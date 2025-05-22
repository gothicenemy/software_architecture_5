package datastore

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	outFileNamePrefix   = "segment-"
	mergeFileNameSuffix = ".merged"
)

var MaxFileSize int64 = 10 * 1024 * 1024

var ErrNotFound = errors.New("record does not exist")
var ErrWrongType = errors.New("incorrect value type")

type indexValue struct {
	segmentID int
	offset    int64
	size      int64
	dataType  byte
}

type Db struct {
	dir             string
	currentIndex    map[string]indexValue
	activeSegment   *os.File
	activeSegmentID int
	segmentFiles    map[int]*os.File
	mu              sync.RWMutex
	putCh           chan putRequest
	doneCh          chan struct{}
	isMerging       bool
	mergeMu         sync.Mutex
}

type putRequest struct {
	key      string
	value    string
	valueInt int64
	dataType byte
	errCh    chan error
}

func NewDb(dir string) (*Db, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create db directory %s: %w", dir, err)
	}
	db := &Db{
		dir:          dir,
		currentIndex: make(map[string]indexValue),
		segmentFiles: make(map[int]*os.File),
		putCh:        make(chan putRequest, 100),
		doneCh:       make(chan struct{}),
	}
	if err := db.loadSegmentsAndBuildIndex(); err != nil {
		for _, f := range db.segmentFiles {
			_ = f.Close()
		}
		if db.activeSegment != nil {
			_ = db.activeSegment.Close()
		}
		return nil, fmt.Errorf("failed to load segments and build index: %w", err)
	}
	go db.processPuts()
	go db.periodicMerge()
	return db, nil
}

func (db *Db) loadSegmentsAndBuildIndex() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	files, err := filepath.Glob(filepath.Join(db.dir, outFileNamePrefix+"*"))
	if err != nil {
		return fmt.Errorf("failed to glob segment files: %w", err)
	}
	segmentIDs := make([]int, 0, len(files))
	segmentFilePaths := make(map[int]string)
	for _, filePath := range files {
		baseName := filepath.Base(filePath)
		if strings.HasSuffix(baseName, mergeFileNameSuffix) || strings.HasSuffix(baseName, ".tmp") {
			_ = os.Remove(filePath)
			continue
		}
		segIDStr := strings.TrimPrefix(baseName, outFileNamePrefix)
		segID, errConv := strconv.Atoi(segIDStr)
		if errConv != nil {
			continue
		}
		segmentIDs = append(segmentIDs, segID)
		segmentFilePaths[segID] = filePath
	}
	sort.Ints(segmentIDs)
	maxSegID := -1
	for _, segID := range segmentIDs {
		filePath := segmentFilePaths[segID]
		file, openErr := os.OpenFile(filePath, os.O_RDONLY, 0644)
		if openErr != nil {
			return fmt.Errorf("failed to open segment file %s for reading: %w", filePath, openErr)
		}
		db.segmentFiles[segID] = file
		if loadErr := db.loadIndexFromSegmentFile(file, segID); loadErr != nil {
			return fmt.Errorf("failed to load index from segment %d (%s): %w", segID, filePath, loadErr)
		}
		if segID > maxSegID {
			maxSegID = segID
		}
	}
	db.activeSegmentID = maxSegID + 1
	if maxSegID == -1 {
		db.activeSegmentID = 0
	}
	return db.setActiveSegment(db.activeSegmentID)
}

func (db *Db) loadIndexFromSegmentFile(file *os.File, segID int) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek to start of segment %d (%s): %w", segID, file.Name(), err)
	}
	reader := bufio.NewReader(file)
	var currentOffset int64 = 0
	for {
		record := entry{}
		bytesRead, err := record.DecodeFromReader(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("error decoding entry from segment %d (%s) at offset %d: %w", segID, file.Name(), currentOffset, err)
		}
		db.currentIndex[record.key] = indexValue{
			segmentID: segID,
			offset:    currentOffset,
			size:      int64(bytesRead),
			dataType:  record.dataType,
		}
		currentOffset += int64(bytesRead)
	}
	return nil
}

func (db *Db) setActiveSegment(segID int) error {
	if db.activeSegment != nil {
		if err := db.activeSegment.Close(); err != nil {
			fmt.Printf("Warning: setActiveSegment: failed to close previous active segment %d: %v\n", db.activeSegmentID, err)
		}
		db.activeSegment = nil
	}
	filePath := filepath.Join(db.dir, fmt.Sprintf("%s%d", outFileNamePrefix, segID))
	writeFile, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("setActiveSegment: failed to open/create segment %d (%s) for writing: %w", segID, filePath, err)
	}
	db.activeSegment = writeFile
	db.activeSegmentID = segID

	if oldReadFile, exists := db.segmentFiles[segID]; exists {
		_ = oldReadFile.Close()
	}
	readFile, err := os.OpenFile(filePath, os.O_RDONLY, 0644)
	if err != nil {
		_ = db.activeSegment.Close()
		db.activeSegment = nil
		return fmt.Errorf("setActiveSegment: failed to open segment %d (%s) for reading: %w", segID, filePath, err)
	}
	db.segmentFiles[segID] = readFile
	return nil
}

func (db *Db) processPuts() {
	for {
		select {
		case req := <-db.putCh:
			db.mu.Lock()
			e := entry{key: req.key, dataType: req.dataType}
			if req.dataType == DataTypeString {
				e.value = req.value
			} else {
				e.valueInt = req.valueInt
			}
			encodedEntry := e.Encode()
			recordSize := int64(len(encodedEntry))
			var writeErr error
			var currentOffset int64

			if db.activeSegment == nil {
				writeErr = errors.New("processPuts: active segment is nil, cannot write")
			} else {
				stat, statErr := db.activeSegment.Stat()
				if statErr != nil {
					writeErr = fmt.Errorf("processPuts: failed to get active segment stat: %w", statErr)
				} else {
					currentOffset = stat.Size()
					if currentOffset+recordSize > MaxFileSize && MaxFileSize > 0 {
						if setActiveErr := db.setActiveSegment(db.activeSegmentID + 1); setActiveErr != nil {
							writeErr = fmt.Errorf("processPuts: failed to rotate to new segment: %w", setActiveErr)
						} else {
							newStat, newStatErr := db.activeSegment.Stat()
							if newStatErr != nil {
								writeErr = fmt.Errorf("processPuts: failed to get new active segment stat: %w", newStatErr)
							} else {
								currentOffset = newStat.Size()
							}
						}
					}
				}
				if writeErr == nil {
					if _, errWrite := db.activeSegment.Write(encodedEntry); errWrite != nil {
						writeErr = fmt.Errorf("processPuts: failed to write entry to active segment %d: %w", db.activeSegmentID, errWrite)
					} else {
						db.currentIndex[req.key] = indexValue{
							segmentID: db.activeSegmentID,
							offset:    currentOffset,
							size:      recordSize,
							dataType:  req.dataType,
						}
					}
				}
			}
			db.mu.Unlock()
			if req.errCh != nil {
				req.errCh <- writeErr
			}
		case <-db.doneCh:
			return
		}
	}
}

func (db *Db) Put(key string, value string) error {
	errCh := make(chan error, 1)
	req := putRequest{
		key:      key,
		value:    value,
		dataType: DataTypeString,
		errCh:    errCh,
	}
	select {
	case db.putCh <- req:
		return <-errCh
	case <-db.doneCh:
		return errors.New("database is closed")
	}
}

func (db *Db) PutInt64(key string, value int64) error {
	errCh := make(chan error, 1)
	req := putRequest{
		key:      key,
		valueInt: value,
		dataType: DataTypeInt64,
		errCh:    errCh,
	}
	select {
	case db.putCh <- req:
		return <-errCh
	case <-db.doneCh:
		return errors.New("database is closed")
	}
}

func (db *Db) Get(key string) (string, error) {
	db.mu.RLock()
	idxVal, ok := db.currentIndex[key]
	if !ok {
		db.mu.RUnlock()
		return "", ErrNotFound
	}
	segmentFile, fileOk := db.segmentFiles[idxVal.segmentID]
	if !fileOk {
		db.mu.RUnlock()
		return "", fmt.Errorf("internal error: segment file %d for key '%s' not found in map (possibly stale or merged)", idxVal.segmentID, key)
	}
	if idxVal.dataType != DataTypeString {
		db.mu.RUnlock()
		return "", ErrWrongType
	}
	recordBytes := make([]byte, idxVal.size)
	_, err := segmentFile.ReadAt(recordBytes, idxVal.offset)
	db.mu.RUnlock()
	if err != nil {
		return "", fmt.Errorf("failed to read entry for key '%s' from segment %d: %w", key, idxVal.segmentID, err)
	}
	record := entry{}
	if errDecode := record.Decode(recordBytes); errDecode != nil {
		return "", fmt.Errorf("failed to decode entry for key '%s': %w", key, errDecode)
	}
	return record.value, nil
}

func (db *Db) GetInt64(key string) (int64, error) {
	db.mu.RLock()
	idxVal, ok := db.currentIndex[key]
	if !ok {
		db.mu.RUnlock()
		return 0, ErrNotFound
	}
	segmentFile, fileOk := db.segmentFiles[idxVal.segmentID]
	if !fileOk {
		db.mu.RUnlock()
		return 0, fmt.Errorf("internal error: segment file %d for key '%s' not found in map (possibly stale or merged)", idxVal.segmentID, key)
	}
	if idxVal.dataType != DataTypeInt64 {
		db.mu.RUnlock()
		return 0, ErrWrongType
	}
	recordBytes := make([]byte, idxVal.size)
	_, err := segmentFile.ReadAt(recordBytes, idxVal.offset)
	db.mu.RUnlock()
	if err != nil {
		return 0, fmt.Errorf("failed to read entry for key '%s' from segment %d: %w", key, idxVal.segmentID, err)
	}
	record := entry{}
	if errDecode := record.Decode(recordBytes); errDecode != nil {
		return 0, fmt.Errorf("failed to decode entry for key '%s': %w", key, errDecode)
	}
	return record.valueInt, nil
}

func (db *Db) Close() error {
	select {
	case <-db.doneCh:
	default:
		close(db.doneCh)
	}
	time.Sleep(50 * time.Millisecond)
	db.mu.Lock()
	defer db.mu.Unlock()
	var firstErr error
	if db.activeSegment != nil {
		if err := db.activeSegment.Close(); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
		db.activeSegment = nil
	}
	for _, file := range db.segmentFiles {
		if err := file.Close(); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	db.segmentFiles = make(map[int]*os.File)
	return firstErr
}

func (db *Db) periodicMerge() {
	mergeInterval := 10 * time.Second
	if os.Getenv("TEST_MERGE_INTERVAL_MS") != "" {
		if ms, err := strconv.Atoi(os.Getenv("TEST_MERGE_INTERVAL_MS")); err == nil && ms > 0 {
			mergeInterval = time.Duration(ms) * time.Millisecond
		}
	}
	ticker := time.NewTicker(mergeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := db.tryMergeSegments(); err != nil {
				fmt.Printf("Error during periodic merge: %v\n", err)
			}
		case <-db.doneCh:
			return
		}
	}
}

func (db *Db) tryMergeSegments() error {
	db.mergeMu.Lock()
	if db.isMerging {
		db.mergeMu.Unlock()
		return nil
	}
	db.isMerging = true
	db.mergeMu.Unlock()
	defer func() {
		db.mergeMu.Lock()
		db.isMerging = false
		db.mergeMu.Unlock()
	}()
	return db.performMerge()
}

func (db *Db) performMerge() error {
	db.mu.Lock()
	defer db.mu.Unlock()

	segmentsToMergeIDs := make([]int, 0)
	for segID := range db.segmentFiles {
		if segID != db.activeSegmentID {
			segmentsToMergeIDs = append(segmentsToMergeIDs, segID)
		}
	}
	sort.Ints(segmentsToMergeIDs)

	if len(segmentsToMergeIDs) < 2 {
		return nil
	}

	targetMergeSegmentID := segmentsToMergeIDs[0]
	mergedFilePathTemp := filepath.Join(db.dir, fmt.Sprintf("%s%d%s.tmp", outFileNamePrefix, targetMergeSegmentID, mergeFileNameSuffix))
	mergedFile, err := os.OpenFile(mergedFilePathTemp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("merge: failed to create temp merged file '%s': %w", mergedFilePathTemp, err)
	}

	newIndexForMergedSegment := make(map[string]indexValue)
	var currentMergedOffset int64 = 0

	for key, idxVal := range db.currentIndex {
		isMerging := false
		for _, mergingID := range segmentsToMergeIDs {
			if idxVal.segmentID == mergingID {
				isMerging = true
				break
			}
		}
		if !isMerging {
			continue
		}
		sourceSegmentFile, ok := db.segmentFiles[idxVal.segmentID]
		if !ok {
			_ = mergedFile.Close()
			_ = os.Remove(mergedFilePathTemp)
			return fmt.Errorf("merge: source segment %d for key '%s' not found in map", idxVal.segmentID, key)
		}
		entryData := make([]byte, idxVal.size)
		if _, readErr := sourceSegmentFile.ReadAt(entryData, idxVal.offset); readErr != nil {
			_ = mergedFile.Close()
			_ = os.Remove(mergedFilePathTemp)
			return fmt.Errorf("merge: failed to read entry for key '%s' from segment %d: %w", key, idxVal.segmentID, readErr)
		}
		if _, writeErr := mergedFile.Write(entryData); writeErr != nil {
			_ = mergedFile.Close()
			_ = os.Remove(mergedFilePathTemp)
			return fmt.Errorf("merge: failed to write entry for key '%s' to merged file: %w", key, writeErr)
		}
		newIndexForMergedSegment[key] = indexValue{
			segmentID: targetMergeSegmentID,
			offset:    currentMergedOffset,
			size:      idxVal.size,
			dataType:  idxVal.dataType,
		}
		currentMergedOffset += idxVal.size
	}

	if syncErr := mergedFile.Sync(); syncErr != nil {
		_ = mergedFile.Close()
		_ = os.Remove(mergedFilePathTemp)
		return fmt.Errorf("merge: failed to sync temp merged file: %w", syncErr)
	}
	if closeErr := mergedFile.Close(); closeErr != nil {
		_ = os.Remove(mergedFilePathTemp)
		return fmt.Errorf("merge: failed to close temp merged file: %w", closeErr)
	}

	finalMergedFilePath := filepath.Join(db.dir, fmt.Sprintf("%s%d", outFileNamePrefix, targetMergeSegmentID))

	if oldTargetFile, ok := db.segmentFiles[targetMergeSegmentID]; ok {
		if errClose := oldTargetFile.Close(); errClose != nil {
			fmt.Printf("Warning: merge: error closing old target file handle %s: %v\n", oldTargetFile.Name(), errClose)
		}
	}
	// Видаляємо старий цільовий файл перед перейменуванням, щоб уникнути проблем на Windows
	if errRemoveOld := os.Remove(finalMergedFilePath); errRemoveOld != nil && !os.IsNotExist(errRemoveOld) {
		_ = os.Remove(mergedFilePathTemp)
		return fmt.Errorf("merge: failed to remove old target file '%s' before rename: %w", finalMergedFilePath, errRemoveOld)
	}

	if renameErr := os.Rename(mergedFilePathTemp, finalMergedFilePath); renameErr != nil {
		_ = os.Remove(mergedFilePathTemp)
		return fmt.Errorf("merge: failed to rename temp merged file '%s' to '%s': %w", mergedFilePathTemp, finalMergedFilePath, renameErr)
	}

	mergedSegmentReadOnly, openErr := os.OpenFile(finalMergedFilePath, os.O_RDONLY, 0644)
	if openErr != nil {
		return fmt.Errorf("merge: CRITICAL: failed to open final merged segment '%s' for reading after rename: %w", finalMergedFilePath, openErr)
	}

	for key, val := range newIndexForMergedSegment {
		db.currentIndex[key] = val
	}
	delete(db.segmentFiles, targetMergeSegmentID) // Видаляємо старий дескриптор, якщо був
	db.segmentFiles[targetMergeSegmentID] = mergedSegmentReadOnly

	for _, segIDToRemove := range segmentsToMergeIDs {
		if segIDToRemove == targetMergeSegmentID {
			continue
		}
		if oldFile, ok := db.segmentFiles[segIDToRemove]; ok {
			_ = oldFile.Close()
			delete(db.segmentFiles, segIDToRemove)
			filePathToRemove := filepath.Join(db.dir, fmt.Sprintf("%s%d", outFileNamePrefix, segIDToRemove))
			if removeErr := os.Remove(filePathToRemove); removeErr != nil {
				fmt.Printf("Warning: merge: failed to remove old segment file %s: %v\n", filePathToRemove, removeErr)
			}
		}
	}
	return nil
}

func (db *Db) Size() (int64, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	var totalSize int64
	files, err := filepath.Glob(filepath.Join(db.dir, outFileNamePrefix+"*"))
	if err != nil {
		return 0, fmt.Errorf("size: failed to glob segment files: %w", err)
	}
	for _, filePath := range files {
		if strings.HasSuffix(filePath, mergeFileNameSuffix) || strings.HasSuffix(filePath, ".tmp") {
			continue
		}
		info, statErr := os.Stat(filePath)
		if statErr != nil {
			continue
		}
		totalSize += info.Size()
	}
	return totalSize, nil
}

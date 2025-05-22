package datastore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// setTestMergeInterval встановлює змінну середовища для інтервалу злиття та повертає її попереднє значення.
func setTestMergeInterval(t *testing.T, intervalMs string) (originalInterval string) {
	t.Helper()
	originalInterval = os.Getenv("TEST_MERGE_INTERVAL_MS")
	os.Setenv("TEST_MERGE_INTERVAL_MS", intervalMs)
	return
}

// setupTestDb створює тестову БД.
// disablePeriodicMerge: якщо true, встановлює дуже великий інтервал для фонового злиття.
func setupTestDb(t *testing.T, disablePeriodicMerge bool) (*Db, func()) {
	t.Helper()
	dir := t.TempDir()
	originalMaxFileSize := MaxFileSize
	MaxFileSize = 1024 // 1KB для тестів

	var originalMergeEnv string
	if disablePeriodicMerge {
		originalMergeEnv = setTestMergeInterval(t, "3600000") // 1 година, фактично вимикає periodicMerge
	} else {
		originalMergeEnv = setTestMergeInterval(t, "100") // 100ms
	}

	db, err := NewDb(dir)
	if err != nil {
		t.Fatalf("Failed to open DB: %v", err)
	}

	cleanup := func() {
		time.Sleep(300 * time.Millisecond)
		if errDbClose := db.Close(); errDbClose != nil {
			t.Logf("Error closing DB during cleanup: %v", errDbClose)
		}
		MaxFileSize = originalMaxFileSize
		os.Setenv("TEST_MERGE_INTERVAL_MS", originalMergeEnv)
	}
	return db, cleanup
}

func TestDb_Put_Get_String(t *testing.T) {
	db, cleanup := setupTestDb(t, true)
	defer cleanup()

	key := "testKey"
	value := "testValue"

	if err := db.Put(key, value); err != nil {
		t.Fatalf("Put failed: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	retrievedValue, err := db.Get(key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if retrievedValue != value {
		t.Errorf("Get returned wrong value: got '%s', want '%s'", retrievedValue, value)
	}

	_, err = db.Get("nonExistentKey")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Expected ErrNotFound for non-existent key, got %v", err)
	}
}

func TestDb_Put_Get_Int64(t *testing.T) {
	db, cleanup := setupTestDb(t, true)
	defer cleanup()

	key := "intKey"
	var value int64 = 1234567890

	if err := db.PutInt64(key, value); err != nil {
		t.Fatalf("PutInt64 failed: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	retrievedValue, err := db.GetInt64(key)
	if err != nil {
		t.Fatalf("GetInt64 failed: %v", err)
	}
	if retrievedValue != value {
		t.Errorf("GetInt64 returned wrong value: got %d, want %d", retrievedValue, value)
	}

	_, err = db.GetInt64("nonExistentIntKey")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Expected ErrNotFound for non-existent int key, got %v", err)
	}

	if err := db.Put("stringKeyForIntTest", "not_an_int"); err != nil {
		t.Fatalf("Put string failed: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	_, err = db.GetInt64("stringKeyForIntTest")
	if !errors.Is(err, ErrWrongType) {
		t.Errorf("Expected ErrWrongType when getting string as int, got %v", err)
	}

	_, err = db.Get(key)
	if !errors.Is(err, ErrWrongType) {
		t.Errorf("Expected ErrWrongType when getting int as string, got %v", err)
	}
}

func TestDb_Persistence(t *testing.T) {
	dir := t.TempDir()
	originalMaxFileSize := MaxFileSize
	MaxFileSize = 1024
	originalMergeEnv := setTestMergeInterval(t, "3600000")
	defer func() {
		MaxFileSize = originalMaxFileSize
		os.Setenv("TEST_MERGE_INTERVAL_MS", originalMergeEnv)
	}()

	db, err := NewDb(dir)
	if err != nil {
		t.Fatalf("Failed to open DB for the first time: %v", err)
	}

	pairs := map[string]string{
		"key1": "value1",
		"key2": "value2",
	}
	intPairs := map[string]int64{
		"intKey1": 111,
		"intKey2": 222,
	}

	for k, v := range pairs {
		if err := db.Put(k, v); err != nil {
			t.Fatalf("Put(%s, %s) failed: %v", k, v, err)
		}
	}
	for k, v := range intPairs {
		if err := db.PutInt64(k, v); err != nil {
			t.Fatalf("PutInt64(%s, %d) failed: %v", k, v, err)
		}
	}
	time.Sleep(250 * time.Millisecond)

	if err := db.Put("key1", "value1_updated"); err != nil {
		t.Fatalf("Put update failed: %v", err)
	}
	pairs["key1"] = "value1_updated"
	time.Sleep(150 * time.Millisecond)

	if errDbClose := db.Close(); errDbClose != nil {
		t.Fatalf("Failed to close DB: %v", errDbClose)
	}
	time.Sleep(100 * time.Millisecond) // Додаткова пауза перед відкриттям

	setTestMergeInterval(t, "3600000") // Переконуємося, що periodicMerge вимкнено і для db2
	db2, err2 := NewDb(dir)
	if err2 != nil {
		t.Fatalf("Failed to reopen DB: %v", err2)
	}
	defer db2.Close()

	for k, expectedV := range pairs {
		v, getErr := db2.Get(k)
		if getErr != nil {
			t.Errorf("Get(%s) after reopen failed: %v", k, getErr)
			continue
		}
		if v != expectedV {
			t.Errorf("Get(%s) after reopen: got '%s', want '%s'", k, v, expectedV)
		}
	}
	for k, expectedV := range intPairs {
		v, getErr := db2.GetInt64(k)
		if getErr != nil {
			t.Errorf("GetInt64(%s) after reopen failed: %v", k, getErr)
			continue
		}
		if v != expectedV {
			t.Errorf("GetInt64(%s) after reopen: got %d, want %d", k, v, expectedV)
		}
	}
}

func TestDb_Segmentation(t *testing.T) {
	db, cleanup := setupTestDb(t, true) // ВИМИКАЄМО periodicMerge для цього тесту
	defer cleanup()

	numRecordsToCauseOneRotation := (int(MaxFileSize) / 30) + 5 // ~39 записів для однієї ротації

	numberOfRotations := 3
	for i := 0; i < numRecordsToCauseOneRotation*numberOfRotations; i++ {
		key := fmt.Sprintf("testSegKey%03d", i)
		value := fmt.Sprintf("value%03d", i)
		if err := db.Put(key, value); err != nil {
			t.Fatalf("Put failed for key %s: %v", key, err)
		}
	}
	time.Sleep(600 * time.Millisecond)

	db.mu.RLock()
	finalActiveSegID := db.activeSegmentID
	var actualFileCountOnDisk int
	filesInDir, _ := filepath.Glob(filepath.Join(db.dir, outFileNamePrefix+"*"))
	for _, fPath := range filesInDir {
		if !strings.HasSuffix(fPath, mergeFileNameSuffix) && !strings.HasSuffix(fPath, ".tmp") {
			actualFileCountOnDisk++
		}
	}
	db.mu.RUnlock()

	t.Logf("Segmentation: Final active segment ID after puts: %d, actual files on disk: %d",
		finalActiveSegID, actualFileCountOnDisk)

	// Якщо finalActiveSegID це N, то файли 0, 1, ..., N-1 повинні бути заповнені (або частково заповнені),
	// і файл N сам (активний) повинен бути створений (можливо, порожній).
	// Таким чином, загальна кількість файлів на диску має бути N + 1.
	// В нашому випадку, якщо відбулося numberOfRotations (=3) повних ротацій,
	// то finalActiveSegID має бути numberOfRotations (=3).
	// І файлів на диску має бути numberOfRotations + 1 (=4): segment-0, segment-1, segment-2, segment-3 (активний, порожній).
	expectedFinalActiveID := numberOfRotations
	if finalActiveSegID != expectedFinalActiveID {
		t.Errorf("Expected finalActiveSegID to be %d (implying %d rotations), but got %d.",
			expectedFinalActiveID, numberOfRotations, finalActiveSegID)
	}

	expectedNumberOfFilesOnDisk := expectedFinalActiveID + 1
	if actualFileCountOnDisk != expectedNumberOfFilesOnDisk {
		t.Errorf("Expected %d segment files on disk (0 to %d, including active file %d), got %d.",
			expectedNumberOfFilesOnDisk, finalActiveSegID, finalActiveSegID, actualFileCountOnDisk)
	}

	keyFirstSegment := "testSegKey000"
	valFirstSegment, err := db.Get(keyFirstSegment)
	if err != nil {
		t.Errorf("Failed to get key from supposed first segment (%s): %v", keyFirstSegment, err)
	} else if valFirstSegment != "value000" {
		t.Errorf("Wrong value for %s: got '%s', want 'value000'", keyFirstSegment, valFirstSegment)
	}

	lastKeyIndex := numRecordsToCauseOneRotation*numberOfRotations - 1
	keyLastWritten := fmt.Sprintf("testSegKey%03d", lastKeyIndex)
	expectedValLastWritten := fmt.Sprintf("value%03d", lastKeyIndex)
	valLastWritten, err := db.Get(keyLastWritten)
	if err != nil {
		t.Errorf("Failed to get last written key (%s): %v", keyLastWritten, err)
	} else if valLastWritten != expectedValLastWritten {
		t.Errorf("Wrong value for last written key %s: got '%s', want '%s'", keyLastWritten, valLastWritten, expectedValLastWritten)
	}
}

func TestDb_MergeSegments(t *testing.T) {
	db, cleanup := setupTestDb(t, false)
	defer cleanup()

	recordsPerSegmentFill := (int(MaxFileSize) / 30) + 10

	t.Logf("TestDb_MergeSegments: Populating segment 0...")
	if err := db.Put("keyA", "valA_s0"); err != nil {
		t.Fatal(err)
	}
	if err := db.Put("keyB", "valB_s0"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < recordsPerSegmentFill; i++ {
		if err := db.Put(fmt.Sprintf("pad0_%02d", i), "padding"); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(400 * time.Millisecond)
	db.mu.RLock()
	t.Logf("TestDb_MergeSegments: After populating segment 0, activeSegmentID: %d", db.activeSegmentID)
	db.mu.RUnlock()

	t.Logf("TestDb_MergeSegments: Populating segment 1...")
	if err := db.Put("keyA", "valA_s1_latest"); err != nil {
		t.Fatal(err)
	}
	if err := db.Put("keyC", "valC_s1"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < recordsPerSegmentFill; i++ {
		if err := db.Put(fmt.Sprintf("pad1_%02d", i), "padding"); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(400 * time.Millisecond)
	db.mu.RLock()
	t.Logf("TestDb_MergeSegments: After populating segment 1, activeSegmentID: %d", db.activeSegmentID)
	db.mu.RUnlock()

	t.Logf("TestDb_MergeSegments: Populating segment 2 (active)...")
	if err := db.Put("keyB", "valB_s2_latest"); err != nil {
		t.Fatal(err)
	}
	if err := db.Put("keyD", "valD_s2"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)

	db.mu.RLock()
	activeIDBeforeMerge := db.activeSegmentID
	db.mu.RUnlock()
	t.Logf("TestDb_MergeSegments: BEFORE merge call, current db.activeSegmentID is: %d", activeIDBeforeMerge)

	if activeIDBeforeMerge != 2 {
		t.Fatalf("TestDb_MergeSegments: Pre-condition failed. Expected activeSegmentID to be 2 before merge, but got %d. Test setup (Puts/Sleeps) needs adjustment.", activeIDBeforeMerge)
	}

	if err := db.tryMergeSegments(); err != nil {
		t.Fatalf("tryMergeSegments failed: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	expectedValues := map[string]string{
		"keyA": "valA_s1_latest",
		"keyB": "valB_s2_latest",
		"keyC": "valC_s1",
		"keyD": "valD_s2",
	}

	for k, expectedV := range expectedValues {
		v, err := db.Get(k)
		if err != nil {
			t.Errorf("After merge (activeIDStore was %d), Get(%s) failed: %v", activeIDBeforeMerge, k, err)
			continue
		}
		if v != expectedV {
			t.Errorf("After merge (activeIDStore was %d), Get(%s): got '%s', want '%s'", activeIDBeforeMerge, k, v, expectedV)
		}
	}

	db.mu.RLock()
	var actualFileCountOnDisk int
	filesInDir, _ := filepath.Glob(filepath.Join(db.dir, outFileNamePrefix+"*"))
	var remainingFiles []string
	for _, fPath := range filesInDir {
		if !strings.HasSuffix(fPath, mergeFileNameSuffix) && !strings.HasSuffix(fPath, ".tmp") {
			actualFileCountOnDisk++
			remainingFiles = append(remainingFiles, filepath.Base(fPath))
		}
	}
	finalActiveIDAfterMerge := db.activeSegmentID
	db.mu.RUnlock()

	t.Logf("TestDb_MergeSegments: Files after merge: %v, final active segment ID: %d (was %d before merge call)",
		remainingFiles, finalActiveIDAfterMerge, activeIDBeforeMerge)

	if actualFileCountOnDisk != 2 {
		t.Errorf("Expected 2 segment files after merge (merged 0 and active 2), got %d. Files: %v", actualFileCountOnDisk, remainingFiles)
	}
	if finalActiveIDAfterMerge != 2 {
		t.Errorf("Expected active segment ID to be 2 after merge, got %d", finalActiveIDAfterMerge)
	}
	_, statErr1 := os.Stat(filepath.Join(db.dir, outFileNamePrefix+"1"))
	if !os.IsNotExist(statErr1) {
		t.Errorf("Expected segment-1 to be deleted, but stat returned: %v (file might still exist)", statErr1)
	}
	_, statErr0 := os.Stat(filepath.Join(db.dir, outFileNamePrefix+"0"))
	if statErr0 != nil {
		t.Errorf("Expected segment-0 (merged) to exist, but stat returned: %v", statErr0)
	}
	_, statErr2 := os.Stat(filepath.Join(db.dir, outFileNamePrefix+"2"))
	if statErr2 != nil {
		t.Errorf("Expected segment-2 (active) to exist, but stat returned: %v", statErr2)
	}
}

func TestDb_Concurrency(t *testing.T) {
	db, cleanup := setupTestDb(t, false)
	defer cleanup()

	numGoroutines := 20
	numPutsPerGoroutine := 10
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(gID int) {
			defer wg.Done()
			for j := 0; j < numPutsPerGoroutine; j++ {
				key := fmt.Sprintf("concKey_g%02d_k%02d", gID, j)
				value := fmt.Sprintf("value_g%02d_k%02d", gID, j)
				if err := db.Put(key, value); err != nil {
					t.Logf("Goroutine %d: Put(%s) failed: %v", gID, key, err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
	time.Sleep(500 * time.Millisecond)

	for i := 0; i < numGoroutines; i++ {
		for j := 0; j < numPutsPerGoroutine; j++ {
			key := fmt.Sprintf("concKey_g%02d_k%02d", i, j)
			expectedValue := fmt.Sprintf("value_g%02d_k%02d", i, j)
			retrievedValue, err := db.Get(key)
			if err != nil {
				t.Errorf("After all Puts: Get(%s) failed: %v", key, err)
				continue
			}
			if retrievedValue != expectedValue {
				t.Errorf("After all Puts: Get(%s): got '%s', want '%s'", key, retrievedValue, expectedValue)
			}
		}
	}
}

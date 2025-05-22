package datastore

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	// DataTypeString позначає, що значення є рядком.
	DataTypeString byte = 0
	// DataTypeInt64 позначає, що значення є int64.
	DataTypeInt64 byte = 1
)

// entry представляє один запис в базі даних.
type entry struct {
	key      string
	value    string // Використовується, якщо dataType == DataTypeString
	valueInt int64  // Використовується, якщо dataType == DataTypeInt64
	dataType byte   // Тип збереженого значення
}

// Формат запису в файлі:
// [загальний розмір запису (uint32)] - 4 байти
// [довжина ключа (uint32)]           - 4 байти
// [ключ (string)]                     - змінна довжина
// [тип даних (byte)]                  - 1 байт
// [довжина значення (uint32)]         - 4 байти
// [значення (bytes)]                  - змінна довжина

// Encode серіалізує запис у байтовий зріз.
func (e *entry) Encode() []byte {
	kl := len(e.key)
	var vl int
	var valueBytes []byte

	switch e.dataType {
	case DataTypeString:
		valueBytes = []byte(e.value)
		vl = len(valueBytes)
	case DataTypeInt64:
		buf := new(bytes.Buffer)
		// Записуємо int64 у little-endian форматі
		_ = binary.Write(buf, binary.LittleEndian, e.valueInt)
		valueBytes = buf.Bytes()
		vl = len(valueBytes) // Зазвичай 8 для int64
	default:
		// Обробка невідомого типу (можна панікувати або повертати помилку)
		panic(fmt.Sprintf("unknown data type: %d", e.dataType))
	}

	// Загальний розмір = 4 (розмір) + 4 (kl) + kl + 1 (dataType) + 4 (vl) + vl
	size := 4 + 4 + kl + 1 + 4 + vl
	res := make([]byte, size)

	binary.LittleEndian.PutUint32(res[0:4], uint32(size))           // Загальний розмір
	binary.LittleEndian.PutUint32(res[4:8], uint32(kl))             // Довжина ключа
	copy(res[8:8+kl], e.key)                                        // Ключ
	res[8+kl] = e.dataType                                          // Тип даних
	binary.LittleEndian.PutUint32(res[8+kl+1:8+kl+1+4], uint32(vl)) // Довжина значення
	copy(res[8+kl+1+4:], valueBytes)                                // Значення

	return res
}

// Decode десеріалізує запис з байтового зрізу.
// Вхідний 'input' повинен містити ВЕСЬ запис, включаючи його розмір на початку.
func (e *entry) Decode(input []byte) error {
	if len(input) < 4 {
		return fmt.Errorf("input too short to read size")
	}
	// fullSize := binary.LittleEndian.Uint32(input[0:4]) // Розмір вже відомий, оскільки передається весь запис

	if len(input) < 8 { // 4 (size) + 4 (kl)
		return fmt.Errorf("input too short to read key length")
	}
	kl := binary.LittleEndian.Uint32(input[4:8])

	keyEndOffset := 8 + int(kl)
	if len(input) < keyEndOffset+1 { // +1 для dataType
		return fmt.Errorf("input too short to read key or data type")
	}
	e.key = string(input[8:keyEndOffset])
	e.dataType = input[keyEndOffset]

	vlOffset := keyEndOffset + 1
	if len(input) < vlOffset+4 { // +4 для value length
		return fmt.Errorf("input too short to read value length")
	}
	vl := binary.LittleEndian.Uint32(input[vlOffset : vlOffset+4])

	valueOffset := vlOffset + 4
	if len(input) < valueOffset+int(vl) {
		return fmt.Errorf("input too short to read value (expected %d, got %d from offset %d)", vl, len(input)-(valueOffset), valueOffset)
	}
	valueBytes := input[valueOffset : valueOffset+int(vl)]

	switch e.dataType {
	case DataTypeString:
		e.value = string(valueBytes)
	case DataTypeInt64:
		if len(valueBytes) != 8 {
			return fmt.Errorf("invalid length for int64 value: expected 8, got %d", len(valueBytes))
		}
		reader := bytes.NewReader(valueBytes)
		if err := binary.Read(reader, binary.LittleEndian, &e.valueInt); err != nil {
			return fmt.Errorf("failed to decode int64 value: %w", err)
		}
	default:
		return fmt.Errorf("unknown data type during decode: %d", e.dataType)
	}
	return nil
}

// DecodeFromReader читає та десеріалізує один запис з bufio.Reader.
// Повертає кількість прочитаних байт та помилку.
func (e *entry) DecodeFromReader(in *bufio.Reader) (int, error) {
	// 1. Читаємо загальний розмір запису
	sizeBuf := make([]byte, 4)
	if _, err := io.ReadFull(in, sizeBuf); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) { // Обробляємо обидва випадки EOF
			return 0, io.EOF // Повертаємо чистий EOF, якщо це кінець файлу
		}
		return 0, fmt.Errorf("failed to read entry size: %w", err)
	}
	entrySize := binary.LittleEndian.Uint32(sizeBuf)

	if entrySize <= 4 { // Розмір має бути більшим за розмір самого поля "розмір"
		return 4, fmt.Errorf("invalid entry size: %d", entrySize)
	}

	// 2. Читаємо решту запису
	// Ми вже прочитали 4 байти (розмір), тому читаємо entrySize - 4
	recordData := make([]byte, entrySize-4)
	if _, err := io.ReadFull(in, recordData); err != nil {
		return 4, fmt.Errorf("failed to read entry data (expected %d bytes): %w", entrySize-4, err)
	}

	// Створюємо повний байтовий зріз для Decode
	fullRecordBytes := make([]byte, entrySize)
	copy(fullRecordBytes[0:4], sizeBuf)
	copy(fullRecordBytes[4:], recordData)

	if err := e.Decode(fullRecordBytes); err != nil {
		return int(entrySize), fmt.Errorf("failed to decode entry from read data: %w", err)
	}

	return int(entrySize), nil
}

// datastore/entry_test.go
package datastore

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"
)

func TestEntry_EncodeDecode_String(t *testing.T) {
	e := entry{key: "testKey", value: "testValue", dataType: DataTypeString}
	encoded := e.Encode()

	var decodedEntry entry
	if err := decodedEntry.Decode(encoded); err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	if decodedEntry.key != e.key {
		t.Errorf("Decoded key mismatch: got %s, want %s", decodedEntry.key, e.key)
	}
	if decodedEntry.dataType != e.dataType {
		t.Errorf("Decoded dataType mismatch: got %d, want %d", decodedEntry.dataType, e.dataType)
	}
	if decodedEntry.value != e.value {
		t.Errorf("Decoded value mismatch: got %s, want %s", decodedEntry.value, e.value)
	}
}

func TestEntry_EncodeDecode_Int64(t *testing.T) {
	e := entry{key: "intKey", valueInt: 12345678912345, dataType: DataTypeInt64}
	encoded := e.Encode()

	var decodedEntry entry
	if err := decodedEntry.Decode(encoded); err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	if decodedEntry.key != e.key {
		t.Errorf("Decoded key mismatch: got %s, want %s", decodedEntry.key, e.key)
	}
	if decodedEntry.dataType != e.dataType {
		t.Errorf("Decoded dataType mismatch: got %d, want %d", decodedEntry.dataType, e.dataType)
	}
	if decodedEntry.valueInt != e.valueInt {
		t.Errorf("Decoded valueInt mismatch: got %d, want %d", decodedEntry.valueInt, e.valueInt)
	}
}

func TestEntry_DecodeFromReader(t *testing.T) {
	testCases := []entry{
		{key: "key1", value: "value1", dataType: DataTypeString},
		{key: "anotherKey", valueInt: -9876543210, dataType: DataTypeInt64},
		{key: "short", value: "s", dataType: DataTypeString},
		{key: "emptyVal", value: "", dataType: DataTypeString},
	}

	for i, tc := range testCases {
		t.Run(fmt.Sprintf("TestCase%d", i), func(t *testing.T) {
			encoded := tc.Encode()
			reader := bufio.NewReader(bytes.NewReader(encoded))

			var decodedEntry entry
			bytesRead, err := decodedEntry.DecodeFromReader(reader)
			if err != nil {
				t.Fatalf("DecodeFromReader failed: %v", err)
			}

			if bytesRead != len(encoded) {
				t.Errorf("BytesRead mismatch: got %d, want %d", bytesRead, len(encoded))
			}
			if decodedEntry.key != tc.key {
				t.Errorf("Decoded key mismatch: got %s, want %s", decodedEntry.key, tc.key)
			}
			if decodedEntry.dataType != tc.dataType {
				t.Errorf("Decoded dataType mismatch: got %d, want %d", decodedEntry.dataType, tc.dataType)
			}
			if tc.dataType == DataTypeString && decodedEntry.value != tc.value {
				t.Errorf("Decoded value (string) mismatch: got %s, want %s", decodedEntry.value, tc.value)
			}
			if tc.dataType == DataTypeInt64 && decodedEntry.valueInt != tc.valueInt {
				t.Errorf("Decoded value (int64) mismatch: got %d, want %d", decodedEntry.valueInt, tc.valueInt)
			}

			// Перевірка, що після читання в буфері нічого не залишилося
			_, err = reader.ReadByte()
			if !errors.Is(err, io.EOF) {
				t.Errorf("Expected EOF after reading entry, but got data or error: %v", err)
			}
		})
	}
}

func TestEntry_Decode_ErrorHandling(t *testing.T) {
	// Тести на пошкоджені дані
	shortInput := []byte{0x0A, 0x00, 0x00, 0x00} // Розмір 10, але даних немає
	var e entry
	err := e.Decode(shortInput)
	if err == nil {
		t.Error("Expected error for short input, got nil")
	}
	// ... більше тестів на пошкоджені дані ...
}

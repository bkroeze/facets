package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

type field struct {
	name  string
	value any
}

type object []field

type table struct {
	columns []string
	rows    [][]any
}

type primitiveArray []any

func writeDocument(w io.Writer, format string, doc object) error {
	var data []byte
	var err error
	if format == "json" {
		data, err = marshalJSON(doc)
	} else {
		var rendered string
		rendered, err = renderTOON(doc)
		data = []byte(rendered)
	}
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

func marshalJSON(value any) ([]byte, error) {
	var b bytes.Buffer
	if err := appendJSON(&b, value); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func appendJSON(b *bytes.Buffer, value any) error {
	switch value := value.(type) {
	case object:
		b.WriteByte('{')
		for i, entry := range value {
			if i > 0 {
				b.WriteByte(',')
			}
			key, _ := json.Marshal(entry.name)
			b.Write(key)
			b.WriteByte(':')
			if err := appendJSON(b, entry.value); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	case table:
		b.WriteByte('[')
		for i, row := range value.rows {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteByte('{')
			for j, column := range value.columns {
				if j > 0 {
					b.WriteByte(',')
				}
				key, _ := json.Marshal(column)
				b.Write(key)
				b.WriteByte(':')
				if err := appendJSON(b, row[j]); err != nil {
					return err
				}
			}
			b.WriteByte('}')
		}
		b.WriteByte(']')
	case primitiveArray:
		b.WriteByte('[')
		for i, item := range value {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := appendJSON(b, item); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case string:
		if !utf8.ValidString(value) {
			return fmt.Errorf("JSON string is not valid UTF-8")
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		b.Write(encoded)
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		b.Write(encoded)
	}
	return nil
}

func renderTOON(doc object) (string, error) {
	var b strings.Builder
	if err := appendTOONObject(&b, doc, 0); err != nil {
		return "", err
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}

func appendTOONObject(b *strings.Builder, doc object, depth int) error {
	indent := strings.Repeat("  ", depth)
	for _, entry := range doc {
		switch value := entry.value.(type) {
		case object:
			fmt.Fprintf(b, "%s%s:\n", indent, entry.name)
			if err := appendTOONObject(b, value, depth+1); err != nil {
				return err
			}
		case table:
			if len(value.rows) == 0 {
				fmt.Fprintf(b, "%s%s: []\n", indent, entry.name)
				continue
			}
			fmt.Fprintf(b, "%s%s[%d]{%s}:\n", indent, entry.name, len(value.rows), strings.Join(value.columns, ","))
			rowIndent := strings.Repeat("  ", depth+1)
			for _, row := range value.rows {
				b.WriteString(rowIndent)
				for i, item := range row {
					if i > 0 {
						b.WriteByte(',')
					}
					scalar, err := toonScalar(item)
					if err != nil {
						return err
					}
					b.WriteString(scalar)
				}
				b.WriteByte('\n')
			}
		case primitiveArray:
			fmt.Fprintf(b, "%s%s[%d]:", indent, entry.name, len(value))
			if len(value) > 0 {
				b.WriteByte(' ')
				for i, item := range value {
					if i > 0 {
						b.WriteByte(',')
					}
					scalar, err := toonScalar(item)
					if err != nil {
						return err
					}
					b.WriteString(scalar)
				}
			}
			b.WriteByte('\n')
		default:
			scalar, err := toonScalar(value)
			if err != nil {
				return err
			}
			fmt.Fprintf(b, "%s%s: %s\n", indent, entry.name, scalar)
		}
	}
	return nil
}

func toonScalar(value any) (string, error) {
	switch value := value.(type) {
	case string:
		return quoteTOON(value)
	case nil:
		return "null", nil
	case bool:
		return strconv.FormatBool(value), nil
	case int:
		return strconv.Itoa(value), nil
	case int64:
		return strconv.FormatInt(value, 10), nil
	case float64:
		return strconv.FormatFloat(value, 'g', -1, 64), nil
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return "", err
		}
		return string(encoded), nil
	}
}

func quoteTOON(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", fmt.Errorf("TOON string is not valid UTF-8")
	}
	var b strings.Builder
	b.Grow(len(value) + 2)
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '\\':
			b.WriteString("\\\\")
		case '"':
			b.WriteString("\\\"")
		case '\n':
			b.WriteString("\\n")
		case '\r':
			b.WriteString("\\r")
		case '\t':
			b.WriteString("\\t")
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, "\\u%04x", r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String(), nil
}

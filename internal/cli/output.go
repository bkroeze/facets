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
	value any
	name  string
}

type object []field

type table struct {
	columns []string
	rows    [][]any
}

type primitiveArray []any
type objectArray []object

func writeDocument(w io.Writer, format string, doc object) error {
	var data []byte
	var err error
	switch format {
	case "json":
		data, err = marshalJSON(doc)
	case "human":
		var rendered string
		rendered, err = renderHuman(doc)
		data = []byte(rendered)
	default:
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

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiCyan   = "\x1b[36m"
	ansiGreen  = "\x1b[32m"
	ansiRed    = "\x1b[31m"
	ansiYellow = "\x1b[33m"
	ansiDim    = "\x1b[2m"
)

func renderHuman(doc object) (string, error) {
	if err := validateHumanValue(doc); err != nil {
		return "", err
	}
	var b strings.Builder
	appendHumanObject(&b, doc, 0)
	return strings.TrimSuffix(b.String(), "\n"), nil
}

func validateHumanValue(value any) error {
	switch value := value.(type) {
	case object:
		for _, entry := range value {
			if err := validateHumanValue(entry.value); err != nil {
				return err
			}
		}
	case table:
		for _, row := range value.rows {
			for _, item := range row {
				if err := validateHumanValue(item); err != nil {
					return err
				}
			}
		}
	case objectArray:
		for _, item := range value {
			if err := validateHumanValue(item); err != nil {
				return err
			}
		}
	case primitiveArray:
		for _, item := range value {
			if err := validateHumanValue(item); err != nil {
				return err
			}
		}
	case string:
		if !utf8.ValidString(value) {
			return fmt.Errorf("human string is not valid UTF-8")
		}
	}
	return nil
}

func appendHumanObject(b *strings.Builder, doc object, depth int) {
	indent := strings.Repeat("  ", depth)
	for _, entry := range doc {
		switch value := entry.value.(type) {
		case object:
			if entry.name == "error" {
				appendHumanError(b, value, indent)
				continue
			}
			if entry.name == "help" {
				appendHumanHelp(b, value, indent)
				continue
			}
			fmt.Fprintf(b, "%s%s%s%s:\n", indent, ansiBold, ansiCyan, entry.name+ansiReset)
			appendHumanObject(b, value, depth+1)
		case table:
			fmt.Fprintf(b, "%s%s%s%s:\n", indent, ansiBold, ansiCyan, entry.name+ansiReset)
			appendHumanTable(b, value, depth+1)
		case objectArray:
			fmt.Fprintf(b, "%s%s%s%s:\n", indent, ansiBold, ansiCyan, entry.name+ansiReset)
			for _, item := range value {
				fmt.Fprintf(b, "%s-\n", strings.Repeat("  ", depth+1))
				appendHumanObject(b, item, depth+2)
			}
		case primitiveArray:
			fmt.Fprintf(b, "%s%s%s%s:\n", indent, ansiBold, ansiCyan, entry.name+ansiReset)
			for _, item := range value {
				fmt.Fprintf(b, "%s- %s\n", strings.Repeat("  ", depth+1), humanScalar(item))
			}
		default:
			fmt.Fprintf(b, "%s%s%s%s: %s\n", indent, ansiBold, ansiCyan, entry.name+ansiReset, humanScalar(value))
		}
	}
}

func appendHumanError(b *strings.Builder, doc object, indent string) {
	kindValue, _ := objectValue(doc, "type")
	kind, _ := kindValue.(string)
	message, _ := objectValue(doc, "message")
	color := ansiRed
	if kind == "usage" {
		color = ansiYellow
	}
	fmt.Fprintf(b, "%s%s%s%s: %s%s\n", indent, ansiBold, color, humanTitle(kind), humanScalar(message), ansiReset)
	if help, ok := objectValue(doc, "help"); ok {
		fmt.Fprintf(b, "%s%sHint:%s %s\n", indent, ansiDim, ansiReset, humanScalar(help))
	}
}

func appendHumanHelp(b *strings.Builder, doc object, indent string) {
	if usage, ok := objectValue(doc, "usage"); ok {
		fmt.Fprintf(b, "%s%s%s%s%s\n", indent, ansiBold, ansiGreen, humanScalar(usage), ansiReset)
	}
	if description, ok := objectValue(doc, "description"); ok {
		fmt.Fprintf(b, "%s%s%s%s\n", indent, ansiDim, humanScalar(description), ansiReset)
	}
	if options, ok := objectValue(doc, "options"); ok {
		if optionsTable, ok := options.(table); ok {
			fmt.Fprintf(b, "%s%sOptions:%s\n", indent, ansiBold, ansiReset)
			appendHumanTable(b, optionsTable, len(indent)/2+1)
		}
	}
	if examples, ok := objectValue(doc, "examples"); ok {
		if items, ok := examples.(primitiveArray); ok {
			fmt.Fprintf(b, "%s%sExamples:%s\n", indent, ansiBold, ansiReset)
			for _, item := range items {
				fmt.Fprintf(b, "%s  %s%s%s\n", indent, ansiDim, humanScalar(item), ansiReset)
			}
		}
	}
	if commands, ok := objectValue(doc, "commands"); ok {
		if items, ok := commands.(primitiveArray); ok {
			fmt.Fprintf(b, "%s%sCommands:%s\n", indent, ansiBold, ansiReset)
			for _, item := range items {
				fmt.Fprintf(b, "%s  %s\n", indent, humanScalar(item))
			}
		}
	}
}

func appendHumanTable(b *strings.Builder, value table, depth int) {
	indent := strings.Repeat("  ", depth)
	widths := make([]int, len(value.columns))
	for i, column := range value.columns {
		widths[i] = len(column)
	}
	rows := make([][]string, len(value.rows))
	for i, row := range value.rows {
		rows[i] = make([]string, len(value.columns))
		for j := range value.columns {
			if j < len(row) {
				rows[i][j] = humanScalar(row[j])
			}
			if len(rows[i][j]) > widths[j] {
				widths[j] = len(rows[i][j])
			}
		}
	}
	fmt.Fprintf(b, "%s%s\n", indent, humanTableRow(value.columns, widths, ansiBold))
	fmt.Fprintf(b, "%s%s\n", indent, humanTableSeparator(widths))
	for _, row := range rows {
		fmt.Fprintf(b, "%s%s\n", indent, humanTableRow(row, widths, ""))
	}
}

func humanTableRow(values []string, widths []int, style string) string {
	var b strings.Builder
	b.WriteString(style)
	for i, value := range values {
		if i > 0 {
			b.WriteString("  ")
		}
		fmt.Fprintf(&b, "%-*s", widths[i], value)
	}
	if style != "" {
		b.WriteString(ansiReset)
	}
	return b.String()
}

func humanTableSeparator(widths []int) string {
	var b strings.Builder
	for i, width := range widths {
		if i > 0 {
			b.WriteString("  ")
		}
		b.WriteString(strings.Repeat("-", width))
	}
	return b.String()
}

func objectValue(doc object, name string) (any, bool) {
	for _, entry := range doc {
		if entry.name == name {
			return entry.value, true
		}
	}
	return nil, false
}

func humanTitle(value string) string {
	if value == "" {
		return "Output"
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func humanScalar(value any) string {
	switch value := value.(type) {
	case nil:
		return "—"
	case string:
		return value
	case bool:
		return strconv.FormatBool(value)
	case int:
		return strconv.Itoa(value)
	case int64:
		return strconv.FormatInt(value, 10)
	case float64:
		return strconv.FormatFloat(value, 'g', -1, 64)
	default:
		return fmt.Sprint(value)
	}
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
	case objectArray:
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
		case objectArray:
			fmt.Fprintf(b, "%s%s[%d]:\n", indent, entry.name, len(value))
			for _, item := range value {
				fmt.Fprintf(b, "%s-\n", strings.Repeat("  ", depth+1))
				if err := appendTOONObject(b, item, depth+2); err != nil {
					return err
				}
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

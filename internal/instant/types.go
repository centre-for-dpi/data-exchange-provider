//go:build !canal

package instant

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Kind is the engine-neutral type of a column. KindUnsupported columns cannot be exposed.
type Kind string

const (
	KindString      Kind = "string"
	KindInteger     Kind = "integer"
	KindNumber      Kind = "number"
	KindBoolean     Kind = "boolean"
	KindDate        Kind = "date"
	KindDateTime    Kind = "datetime"   // no offset in the source
	KindDateTimeTZ  Kind = "datetimetz" // with offset: RFC 3339
	KindUUID        Kind = "uuid"
	KindUnsupported Kind = ""
)

// asKind maps a declaration's `as` to the kind it exposes.
var asKind = map[string]Kind{
	"string": KindString, "integer": KindInteger, "number": KindNumber, "boolean": KindBoolean,
	"date": KindDate, "date-time": KindDateTimeTZ,
}

// conversions lists, for each exposed kind, the catalog kinds that convert to it faithfully.
var conversions = map[Kind][]Kind{
	KindString:     {KindString, KindInteger, KindNumber, KindBoolean, KindDate, KindDateTime, KindDateTimeTZ, KindUUID},
	KindInteger:    {KindInteger},
	KindNumber:     {KindNumber, KindInteger},
	KindBoolean:    {KindBoolean, KindInteger},
	KindDate:       {KindDate, KindDateTime, KindDateTimeTZ},
	KindDateTimeTZ: {KindDateTimeTZ},
}

// exposedKind is the kind a column is served as; false when `as` cannot convert it faithfully.
func exposedKind(c Column, as string) (Kind, bool) {
	if as == "" {
		return c.Kind, true
	}
	target := asKind[as]
	for _, k := range conversions[target] {
		if k == c.Kind {
			return target, true
		}
	}
	return "", false
}

func possibleAs(k Kind) string {
	var out []string
	for _, as := range []string{"string", "integer", "number", "boolean", "date", "date-time"} {
		for _, from := range conversions[asKind[as]] {
			if from == k {
				out = append(out, "as: "+as)
			}
		}
	}
	return strings.Join(out, ", ")
}

// jsonSchema is the JSON Schema of an exposed column (without nullability).
func jsonSchema(c Column, k Kind) map[string]any {
	switch k {
	case KindString:
		s := map[string]any{"type": "string"}
		if c.Kind == KindString && c.MaxLength > 0 {
			s["maxLength"] = c.MaxLength
		}
		return s
	case KindInteger:
		return map[string]any{"type": "integer"}
	case KindNumber:
		return map[string]any{"type": "number"}
	case KindBoolean:
		return map[string]any{"type": "boolean"}
	case KindDate:
		return map[string]any{"type": "string", "format": "date"}
	case KindDateTimeTZ:
		return map[string]any{"type": "string", "format": "date-time"}
	case KindDateTime: // no offset in the source: not an RFC 3339 date-time
		return map[string]any{"type": "string", "pattern": `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?$`}
	case KindUUID:
		return map[string]any{"type": "string", "format": "uuid"}
	}
	return nil
}

var jsonNumber = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// ToJSON converts a scanned value to what the API serves for the column and its `as`. The
// result always marshals to valid JSON: a value that does not fit the kind is served as text.
func ToJSON(c Column, as string, v any) any {
	if v == nil {
		return nil
	}
	target := c.Kind
	if k, ok := asKind[as]; ok {
		target = k
	}
	if target == KindString && c.Kind != KindString {
		return stringify(natural(c.Kind, v))
	}
	if target == KindBoolean && c.Kind == KindInteger {
		switch n := natural(KindInteger, v).(type) {
		case int64:
			return n != 0
		case json.Number:
			return n != "0"
		}
		return natural(KindBoolean, v)
	}
	return natural(target, v)
}

func natural(k Kind, v any) any {
	if b, ok := v.([]byte); ok && k != KindUUID && k != KindBoolean {
		v = string(b)
	}
	switch k {
	case KindString:
		if s, ok := v.(string); ok {
			return s
		}
		return fmt.Sprint(v)
	case KindInteger:
		switch x := v.(type) {
		case int64, int32, int16, int8, int, uint64, uint32, uint16, uint8:
			return x
		case float64:
			if x == math.Trunc(x) && math.Abs(x) < 1<<53 {
				return int64(x)
			}
			return number(strconv.FormatFloat(x, 'f', -1, 64))
		case string:
			if n, err := strconv.ParseInt(x, 10, 64); err == nil {
				return n
			}
			return number(x)
		}
	case KindNumber:
		switch x := v.(type) {
		case int64, int32, int16, int8, int, uint64, uint32, uint16, uint8:
			return x
		case float64:
			return number(strconv.FormatFloat(x, 'f', -1, 64))
		case float32:
			return number(strconv.FormatFloat(float64(x), 'f', -1, 32))
		case string:
			return number(x)
		case *big.Rat:
			return number(x.FloatString(20))
		}
	case KindBoolean:
		switch x := v.(type) {
		case bool:
			return x
		case int64:
			return x != 0
		case []byte:
			if len(x) == 1 && (x[0] == 0 || x[0] == 1) { // MySQL BIT(1)
				return x[0] == 1
			}
			return parseBool(string(x))
		case string:
			return parseBool(x)
		}
	case KindDate:
		switch x := v.(type) {
		case time.Time:
			return x.Format("2006-01-02")
		case string:
			if len(x) >= 10 {
				if _, err := time.Parse("2006-01-02", x[:10]); err == nil {
					return x[:10]
				}
			}
			return x
		}
	case KindDateTime:
		if t, ok := v.(time.Time); ok {
			return t.Format("2006-01-02T15:04:05.999999999")
		}
	case KindDateTimeTZ:
		if t, ok := v.(time.Time); ok {
			return t.Format(time.RFC3339Nano)
		}
	case KindUUID:
		switch x := v.(type) {
		case [16]byte:
			return formatUUID(x[:])
		case []byte:
			if len(x) == 16 {
				return formatUUID(x)
			}
			return strings.ToLower(string(x))
		case string:
			return strings.ToLower(x)
		}
	}
	return stringify(v)
}

// number keeps a decimal exactly as the database gave it, when it is a JSON number.
func number(s string) any {
	s = strings.TrimSpace(s)
	if jsonNumber.MatchString(s) {
		return json.Number(s)
	}
	return s
}

func parseBool(s string) any {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "t", "true":
		return true
	case "0", "f", "false":
		return false
	}
	return s
}

func stringify(v any) any {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return string(x)
	case []byte:
		return string(x)
	case time.Time:
		return x.Format(time.RFC3339Nano)
	}
	return fmt.Sprint(v)
}

func formatUUID(b []byte) string {
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

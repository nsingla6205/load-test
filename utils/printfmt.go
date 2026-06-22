package utils

import (
	"fmt"
	"reflect"
	"strings"
)

func PrintObject(obj interface{}) string {
	return printObjectWithIndent(obj, 0)
}

func printObjectWithIndent(obj interface{}, indent int) string {
	val := reflect.ValueOf(obj)
	if !val.IsValid() {
		return fmt.Sprintf("%s<nil>\n", strings.Repeat("  ", indent))
	}
	for val.Kind() == reflect.Pointer || val.Kind() == reflect.Interface {
		if val.IsNil() {
			return fmt.Sprintf("%s<nil>\n", strings.Repeat("  ", indent))
		}
		val = val.Elem()
	}
	ind := strings.Repeat("  ", indent)
	switch val.Kind() {
	case reflect.Struct:
		out := "\n"
		typ := val.Type()
		for i := 0; i < val.NumField(); i++ {
			field := val.Field(i)
			fieldType := typ.Field(i)
			if fieldType.Anonymous {
				// New line for embedded struct
				out += fmt.Sprintf("\n%s[%s]\n", ind, fieldType.Type)
				out += printObjectWithIndent(field.Interface(), indent+1)
			} else if fieldType.IsExported() {
				out += fmt.Sprintf("%s*%s*:%s", ind, fieldType.Name, printObjectWithIndent(field.Interface(), indent+1))
			} else {
				out += fmt.Sprintf("%s*%s (unexported)*, Type: %s\n", ind, fieldType.Name, fieldType.Type)
			}
		}
		return out
	case reflect.Slice, reflect.Array:
		out := fmt.Sprintf("%s[\n", ind)
		for j := 0; j < val.Len(); j++ {
			out += printObjectWithIndent(val.Index(j).Interface(), indent+1)
		}
		out += fmt.Sprintf("%s]\n", ind)
		return out
	case reflect.Map:
		out := fmt.Sprintf("%s{\n", ind)
		for _, key := range val.MapKeys() {
			out += fmt.Sprintf("%s*%s*:%s", ind+"  ", printObjectWithIndent(key.Interface(), 0), printObjectWithIndent(val.MapIndex(key).Interface(), indent+1))
		}
		out += fmt.Sprintf("%s}\n", ind)
		return out
	default:
		return fmt.Sprintf("%s%v\n", ind, val.Interface())
	}
}

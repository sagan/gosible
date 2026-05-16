package modules

import (
	"fmt"
	"strings"
)

// StringArg retrieves a string argument from the args map.
func StringArg(args Args, key string) (string, bool) {
	v, ok := args[key]
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%v", v), true
}

// BoolArg retrieves a boolean argument from the args map, supporting string aliases like "yes"/"no".
func BoolArg(args Args, key string, defaultVal bool) bool {
	v, ok := args[key]
	if !ok {
		return defaultVal
	}
	switch val := v.(type) {
	case bool:
		return val
	case string:
		l := strings.ToLower(val)
		return l == "yes" || l == "true" || l == "on" || l == "1"
	default:
		return defaultVal
	}
}

// ShellQuote wraps a string in single quotes, escaping any embedded single quotes.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

// FileExists checks if a path exists on the target host.
func FileExists(ctx *ModuleContext, path string) (bool, error) {
	_, _, rc, err := ctx.Connection.RunCommand(fmt.Sprintf("test -e %s", ShellQuote(path)))
	if err != nil {
		return false, err
	}
	return rc == 0, nil
}

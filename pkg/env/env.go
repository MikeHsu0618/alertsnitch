package env

import (
	"os"
	"strconv"
)

func GetEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

func GetEnvAsBool(key string, defaultValue bool) bool {
	valStr := GetEnv(key, "")
	if val, err := strconv.ParseBool(valStr); err == nil {
		return val
	}
	return defaultValue
}

func GetEnvAsInt(key string, defaultValue int) int {
	valStr := GetEnv(key, "")
	if val, err := strconv.Atoi(valStr); err == nil {
		return val
	}
	return defaultValue
}

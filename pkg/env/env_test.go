package env

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetEnv(t *testing.T) {
	t.Setenv("ALERTSNITCH_TEST_STR", "value")
	assert.Equal(t, "value", GetEnv("ALERTSNITCH_TEST_STR", "fallback"))
	assert.Equal(t, "fallback", GetEnv("ALERTSNITCH_TEST_MISSING", "fallback"))
}

func TestGetEnvAsBool(t *testing.T) {
	t.Setenv("ALERTSNITCH_TEST_BOOL", "true")
	assert.True(t, GetEnvAsBool("ALERTSNITCH_TEST_BOOL", false))

	t.Setenv("ALERTSNITCH_TEST_BADBOOL", "notabool")
	assert.True(t, GetEnvAsBool("ALERTSNITCH_TEST_BADBOOL", true), "invalid value falls back to default")
	assert.False(t, GetEnvAsBool("ALERTSNITCH_TEST_MISSING", false))
}

func TestGetEnvAsInt(t *testing.T) {
	t.Setenv("ALERTSNITCH_TEST_INT", "42")
	assert.Equal(t, 42, GetEnvAsInt("ALERTSNITCH_TEST_INT", 1))

	t.Setenv("ALERTSNITCH_TEST_BADINT", "notanint")
	assert.Equal(t, 7, GetEnvAsInt("ALERTSNITCH_TEST_BADINT", 7), "invalid value falls back to default")
	assert.Equal(t, 9, GetEnvAsInt("ALERTSNITCH_TEST_MISSING", 9))
}

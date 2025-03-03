package eventsource

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestSplitLine(t *testing.T) {
	assert := assert.New(t)
	check := func(expectedKey, expectedValue string) func([]byte, []byte) {
		return func(key, val []byte) {
			bkey := []byte(nil)
			bval := []byte(nil)
			if expectedKey != "<nil>" {
				bkey = []byte(expectedKey)
			}
			if expectedValue != "<nil>" {
				bval = []byte(expectedValue)
			}

			assert.Equal(bkey, key)
			assert.Equal(bval, val)
		}
	}
	check("", "bar")(splitLine([]byte(":bar")))
	check("foo", " bar")(splitLine([]byte("foo:  bar")))
	check("foo", "bar")(splitLine([]byte("foo: bar")))
	check("foo", "bar")(splitLine([]byte("foo:bar")))
	check("foo", "")(splitLine([]byte("foo: ")))
	check("foo", "")(splitLine([]byte("foo:")))
	check("foo", "<nil>")(splitLine([]byte("foo")))
}

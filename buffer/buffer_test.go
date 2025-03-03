package buffer

import (
	"github.com/stretchr/testify/assert"
	"io"
	"strings"
	"testing"
)

func TestReadBuffer(t *testing.T) {
	assert := assert.New(t)
	check := func(expectedLine string, expectedErr error) func([]byte, error) {
		return func(line []byte, err error) {
			assert.Equal([]byte(expectedLine), line)
			assert.Equal(expectedErr, err)
		}
	}
	{
		br := New(strings.NewReader("foo\r\nbar"), 4096)
		check("foo", nil)(br.ReadLine())
		check("bar", io.EOF)(br.ReadLine())
		check("", io.EOF)(br.ReadLine())
	}
	{
		br := New(strings.NewReader("foo\rbar"), 4096)
		check("foo", nil)(br.ReadLine())
		check("bar", io.EOF)(br.ReadLine())
		check("", io.EOF)(br.ReadLine())
	}
	{
		br := New(strings.NewReader("foo\nbar"), 4096)
		check("foo", nil)(br.ReadLine())
		check("bar", io.EOF)(br.ReadLine())
		check("", io.EOF)(br.ReadLine())
	}
	{
		br := New(strings.NewReader("foo\r\nbar\n"), 4096)
		check("foo", nil)(br.ReadLine())
		check("bar", nil)(br.ReadLine())
		check("", io.EOF)(br.ReadLine())
	}
	{
		br := New(strings.NewReader("foo\r\nbar\n\r\n"), 4096)
		check("foo", nil)(br.ReadLine())
		check("bar", nil)(br.ReadLine())
		check("", nil)(br.ReadLine())
		check("", io.EOF)(br.ReadLine())
	}
	{
		br := New(strings.NewReader("foobar"), 7)
		check("foobar", io.EOF)(br.ReadLine())
	}
	{
		br := New(strings.NewReader("foobar"), 6)
		check("foobar", ErrBufferFull)(br.ReadLine())
	}
	{
		br := New(strings.NewReader("foobar"), 5)
		check("fooba", ErrBufferFull)(br.ReadLine())
	}
}

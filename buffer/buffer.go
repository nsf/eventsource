package buffer

// I would happily use bufio.Reader, but sadly for EventSource we can't. The server sent events specification
// defines a line as byte sequence ending with \r\n or \r or \n. This means we cannot use bufio.Reader's methods
// to parse it. I've seen people using bufio.Scanner instead, but this is suboptimal. Anyways, hence we create
// our own buffer. All it can do is do buffered io and read lines as defined by server sent events spec.
//
// I'm not inventing much here, using code snippets from "bufio.Reader".

import (
	"errors"
	"io"
)

var errNegativeRead = errors.New("eventsource: reader returned negative count from Read")
var ErrBufferFull = errors.New("eventsource: buffer full")

const minReadBufferSize = 16
const defaultBufSize = 4096
const maxConsecutiveEmptyReads = 100

type ReadBuffer struct {
	buf     []byte
	rd      io.Reader
	r, w    int
	err     error
	maxSize int
	crLine  bool
}

func New(rd io.Reader, maxSize int) *ReadBuffer {
	bufSize := min(maxSize, defaultBufSize)
	return &ReadBuffer{
		buf:     make([]byte, bufSize),
		rd:      rd,
		maxSize: maxSize,
	}
}

func (b *ReadBuffer) grow() bool {
	if len(b.buf) >= b.maxSize {
		return false
	} else {
		newSize := min(b.maxSize, len(b.buf)*2)
		newBuf := make([]byte, newSize)
		copy(newBuf, b.buf)
		b.buf = newBuf
		return true
	}
}

// Function is copied mostly from bufio.Reader, except this readBuffer can grow automatically up to maxSize.
func (b *ReadBuffer) fill() {
	if b.r > 0 {
		copy(b.buf, b.buf[b.r:b.w])
		b.w -= b.r
		b.r = 0
	}

	if b.w >= len(b.buf) {
		// if there is no space left in the buffer, let's try to grow
		if !b.grow() {
			// grow failed, we're at max capacity
			b.err = ErrBufferFull
			return
		}
	}

	// Read new data: try a limited number of times.
	for i := maxConsecutiveEmptyReads; i > 0; i-- {
		n, err := b.rd.Read(b.buf[b.w:])
		if n < 0 {
			panic(errNegativeRead)
		}
		b.w += n
		if err != nil {
			b.err = err
			return
		}
		if n > 0 {
			return
		}
	}
	b.err = io.ErrNoProgress
}

func findCRLF(buf []byte) int {
	for i := 0; i < len(buf); i++ {
		switch buf[i] {
		case '\r', '\n':
			return i
		}
	}
	return -1
}

func (b *ReadBuffer) readErr() error {
	err := b.err
	b.err = nil
	return err
}

func (b *ReadBuffer) ReadLine() (line []byte, err error) {
	s := 0
	for {
		if b.crLine {
			// We've read the line which ends with \r, but \r\n is also valid line ending.
			// Let's skip the \n if that's the case.
			sl := b.buf[b.r:b.w]
			if len(sl) > 0 {
				// This code is only triggered with s == 0. In two possible cases:
				// - If we enter the function with non-empty buffer (b.w > b.r), then s == 0, because it's set to 0 at
				//   the beginning of the function.
				// - If we enter the function with empty buffer, search and error ifs below fail, b.w == b.r,
				//   means s remains 0 and b.fill() will read more data making b.w > b.r, or it will set an error.
				//   On next loop iteration this code will be triggered then (s is still 0).
				b.crLine = false
				if sl[0] == '\n' {
					b.r++
				}
			}
		}

		// Search buffer.
		if i := findCRLF(b.buf[b.r+s : b.w]); i >= 0 {
			i += s
			line = b.buf[b.r : b.r+i]
			b.crLine = b.buf[b.r+i] == '\r'
			b.r += i + 1
			break
		}

		// Pending error?
		if b.err != nil {
			line = b.buf[b.r:b.w]
			b.r = b.w
			err = b.readErr()
			break
		}

		s = b.w - b.r // do not rescan area we scanned before
		b.fill()      // read more
	}
	return
}

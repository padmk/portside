package viewfmt

import (
	"io"
	"unicode/utf8"
)

// FormatWriter passes program output to a terminal with every format
// character that can reorder or hide text (the ones PlainText replaces)
// turned into U+FFFD; control characters and escape sequences pass
// unchanged, for output that keeps its colors. A character split across
// writes, as WebSocket messages split output, is held until the rest
// arrives; Flush writes what is held at the end.
type FormatWriter struct {
	w     io.Writer
	carry []byte
	buf   []byte
}

// NewFormatWriter returns a FormatWriter writing to w.
func NewFormatWriter(w io.Writer) *FormatWriter { return &FormatWriter{w: w} }

// Write writes p with its hiding format characters replaced. It reports
// len(p) once the cleaned text is written, including bytes held for the
// next write.
func (f *FormatWriter) Write(p []byte) (int, error) {
	data := p
	if len(f.carry) > 0 {
		data = append(f.carry, p...)
		f.carry = nil
	}
	out := f.buf[:0]
	for i := 0; i < len(data); {
		b := data[i]
		if b < utf8.RuneSelf {
			out = append(out, b)
			i++
			continue
		}
		if !utf8.FullRune(data[i:]) {
			// The rest of this character comes with the next write.
			f.carry = append([]byte(nil), data[i:]...)
			break
		}
		r, size := utf8.DecodeRune(data[i:])
		if r == utf8.RuneError && size == 1 {
			// An invalid byte passes as it is, as the terminal would get it.
			out = append(out, b)
		} else if hidingRune(r) {
			out = utf8.AppendRune(out, '�')
		} else {
			out = append(out, data[i:i+size]...)
		}
		i += size
	}
	f.buf = out
	if _, err := f.w.Write(out); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Flush writes the bytes of an incomplete character held from the last
// write, which no later write completes.
func (f *FormatWriter) Flush() error {
	if len(f.carry) == 0 {
		return nil
	}
	_, err := f.w.Write(f.carry)
	f.carry = nil
	return err
}

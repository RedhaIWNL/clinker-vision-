package ingest

import (
	"bufio"
	"bytes"
	"io"
)

func decodeMJPEG(reader io.Reader, emit func([]byte) error) error {
	buffered := bufio.NewReaderSize(reader, 64*1024)
	var frame []byte
	var previous byte
	var havePrevious bool

	for {
		current, err := buffered.ReadByte()
		if err != nil {
			if err == io.EOF {
				if len(frame) > 0 {
					return ErrTruncatedJPEG
				}
				return nil
			}
			return err
		}

		if len(frame) == 0 {
			if havePrevious && previous == 0xff && current == 0xd8 {
				frame = []byte{0xff, 0xd8}
				havePrevious = false
				continue
			}
			previous = current
			havePrevious = true
			continue
		}

		frame = append(frame, current)
		if len(frame) >= 2 && bytes.Equal(frame[len(frame)-2:], []byte{0xff, 0xd9}) {
			complete := frame
			frame = nil
			if err := emit(complete); err != nil {
				return err
			}
		}
	}
}

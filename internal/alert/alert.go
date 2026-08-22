// Package alert implements the bounded terminal completion bell.
package alert

import (
	"errors"
	"fmt"
	"io"
)

const MaxCount = 3

// Bell writes terminal bell characters without invoking external programs.
type Bell struct {
	Writer   io.Writer
	Disabled bool
	Count    int
}

// Ring emits between one and three bells, unless disabled.
func (b Bell) Ring() error {
	if b.Disabled {
		return nil
	}
	if b.Writer == nil {
		return errors.New("terminal bell writer is required")
	}
	count := b.Count
	if count == 0 {
		count = 1
	}
	if count < 1 || count > MaxCount {
		return fmt.Errorf("bell count must be between 1 and %d", MaxCount)
	}
	for range count {
		n, err := io.WriteString(b.Writer, "\a")
		if err != nil {
			return fmt.Errorf("write terminal bell: %w", err)
		}
		if n != 1 {
			return fmt.Errorf("write terminal bell: %w", io.ErrShortWrite)
		}
	}
	return nil
}

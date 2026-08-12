package terminal

import (
	"bytes"
	"encoding/base64"

	"github.com/charmbracelet/x/vt"
)

const maxClipboardBytes = 1 << 20

func registerClipboardHandler(emulator *vt.SafeEmulator, deliver func(string)) {
	emulator.RegisterOscHandler(52, func(data []byte) bool {
		value, ok := decodeClipboardWrite(data)
		if ok {
			deliver(value)
		}
		// Clipboard queries and malformed writes are intentionally consumed. A
		// nested process must not read the launch terminal's clipboard through
		// Lisan, and invalid control data must not leak into the visible screen.
		return true
	})
}

func decodeClipboardWrite(data []byte) (string, bool) {
	parts := bytes.SplitN(data, []byte{';'}, 3)
	if len(parts) != 3 || !bytes.Equal(parts[0], []byte("52")) || bytes.Equal(parts[2], []byte{'?'}) {
		return "", false
	}
	if len(parts[2]) > base64.StdEncoding.EncodedLen(maxClipboardBytes) {
		return "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(string(parts[2]))
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(string(parts[2]))
	}
	if err != nil || len(decoded) > maxClipboardBytes {
		return "", false
	}
	return string(decoded), true
}

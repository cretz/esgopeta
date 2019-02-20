package tests

import (
	"bytes"
	"crypto/rand"
)

const randChars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

func randString(n int) (s string) {
	// We accept that a multiple of 64 is %'d on 62 potentially favoring 0 or 1 more, but we don't care
	byts := make([]byte, n)
	if _, err := rand.Read(byts); err != nil {
		panic(err)
	}
	for _, byt := range byts {
		s += string(randChars[int(byt)%len(randChars)])
	}
	return s
}

func removeGunJSWelcome(b []byte) []byte {
	if bytes.Index(b, []byte("Hello wonderful person!")) == 0 {
		b = b[bytes.IndexByte(b, '\n')+1:]
	}
	return b
}

func skipGunJSWelcome(buf *bytes.Buffer) {
	if bytes.Index(buf.Bytes(), []byte("Hello wonderful person!")) == 0 {
		if _, err := buf.ReadBytes('\n'); err != nil {
			panic(err)
		}
	}
}

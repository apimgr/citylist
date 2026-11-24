package i2p

import (
	cryptorand "crypto/rand"
	"math/big"
	"runtime"
)

func runtimeIsWindows() bool {
	return runtime.GOOS == "windows"
}

func randomPort() (int, error) {
	const first = 64000
	const count = 1000
	value, err := cryptorand.Int(cryptorand.Reader, big.NewInt(count))
	if err != nil {
		return 0, err
	}
	return first + int(value.Int64()), nil
}

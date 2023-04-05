package chainsync

import (
	"encoding/hex"
	"encoding/json"
)

type byteSliceJsonHex []byte

func (b byteSliceJsonHex) MarshalJSON() ([]byte, error) {
	return json.Marshal(hex.EncodeToString([]byte(b)))
}

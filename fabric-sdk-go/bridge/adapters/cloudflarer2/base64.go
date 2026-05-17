package cloudflarer2

import "encoding/base64"

// base64Std is the standard-padded alphabet, matching what the rest of the
// fabric uses for byte/wire payloads.
var base64Std = base64.StdEncoding

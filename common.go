// Copyright 2023 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package podpercon

import (
	"crypto/rand"
	"encoding/base32"
	"strings"

	"github.com/Matir/podperconn/log"
)

var (
	logger = log.GetLogger("core")
)

// Generate a pseudorandom 40 bit ID, 8 chars long
func RandID() string {
	buf := make([]byte, 5)
	_, err := rand.Read(buf)
	if err != nil {
		// This should almost never happen
		panic(err)
	}
	return strings.ToLower(base32.HexEncoding.WithPadding(
		base32.NoPadding).EncodeToString(buf))
}

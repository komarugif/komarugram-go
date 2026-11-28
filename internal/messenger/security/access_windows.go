// SPDX-License-Identifier: Unlicense OR MIT

//go:build windows

package security

import "strings"

// diagnose classifies Windows TBS (TPM Base Services) errors so a missing TPM
// reports AccessMissing instead of a raw error string.
func diagnose(err error) Access {
	if err == nil {
		return Access{}
	}
	msg := err.Error()
	low := strings.ToLower(msg)
	if strings.Contains(low, "8028400f") || strings.Contains(low, "tpm not found") || strings.Contains(low, "no tpm") {
		return Access{Kind: AccessMissing}
	}
	return Access{Kind: AccessFailed, Detail: msg}
}

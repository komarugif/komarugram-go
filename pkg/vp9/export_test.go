// SPDX-License-Identifier: Unlicense OR MIT

package vp9

// SetSlowFault has decodes fail as ones over the time limit while fault
// reports true; nil ends it.
func SetSlowFault(fault func() bool) { slowFault = fault }

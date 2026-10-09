// SPDX-License-Identifier: Unlicense OR MIT

package deviceinfo

import (
	"fmt"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// platform is the product and service pack the registry names, and the
// version of the NT kernel.
func platform() (string, string) {
	v := windows.RtlGetVersion()
	var product, servicePack string
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE); err == nil {
		product, _, _ = k.GetStringValue("ProductName")
		servicePack, _, _ = k.GetStringValue("CSDVersion")
		k.Close()
	}
	return windowsName(product, servicePack, v.BuildNumber), fmt.Sprintf("NT %d.%d.%d", v.MajorVersion, v.MinorVersion, v.BuildNumber)
}

// SPDX-License-Identifier: Unlicense OR MIT

//go:build ignore

// build builds libgiohaiku.so, the C++ half of Gio's Haiku driver, which
// a Gio program on Haiku loads from beside itself (app/os_haiku.go).
//
// On Haiku:
//
//	go run build.go -o /path/to/libgiohaiku.so
//
// From another system, with a C++ compiler for Haiku, such as clang with
// --target=x86_64-unknown-haiku and a sysroot copied from Haiku:
//
//	go run build.go -cxx "clang++ --target=x86_64-unknown-haiku --sysroot=/haiku -fuse-ld=lld" -o libgiohaiku.so
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func main() {
	out := flag.String("o", "libgiohaiku.so", "the library to write")
	cxx := flag.String("cxx", "", `the C++ compiler and its flags (default "g++" on Haiku)`)
	flag.Parse()
	compiler := strings.Fields(*cxx)
	if len(compiler) == 0 {
		if runtime.GOOS != "haiku" {
			fmt.Fprintln(os.Stderr, "build: give -cxx, a C++ compiler for Haiku")
			os.Exit(2)
		}
		compiler = []string{"g++"}
	}
	_, self, _, _ := runtime.Caller(0)
	dir := filepath.Dir(self)
	args := append(compiler[1:],
		"-O2", "-Wall", "-std=c++17", "-fPIC", "-shared",
		"-o", *out,
		filepath.Join(dir, "giohaiku.cpp"),
		"-lbe", "-lOSMesa",
	)
	cmd := exec.Command(compiler[0], args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "build:", err)
		os.Exit(1)
	}
}

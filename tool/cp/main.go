package main

import (
	"errors"
	"flag"
	"os"
	"strings"
)

func main() {
	flag.Parse()
	args := flag.Args()
	if len(args) != 2 {
		println("need 2 parameters")
		os.Exit(1)
		return
	}
	srcFiles := strings.Split(args[0], ",")
	dstFiles := strings.Split(args[1], ",")

	for i := 0; i < len(srcFiles); i++ {
		if err := cp(srcFiles[i], dstFiles[i]); err != nil {
			println(err.Error())
			os.Exit(1)
			return
		}
	}
}

func cp(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return errors.New("stat source file failed: " + err.Error())
	}

	data, err := os.ReadFile(src)
	if err != nil {
		return errors.New("read source file failed: " + err.Error())
	}

	err = os.WriteFile(dst, data, srcInfo.Mode())
	if err != nil {
		return errors.New("write destination file failed: " + err.Error())
	}
	return nil
}

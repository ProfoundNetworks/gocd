// +build ignore
//
// Generator to package `data` datasets using vfsgen
//

package main

import (
	"log"
	"net/http"

	"github.com/shurcooL/vfsgen"
)

func main() {
	var fs http.FileSystem = http.Dir("data")
	err := vfsgen.Generate(fs, vfsgen.Options{
		PackageName: "gocd",
	})
	if err != nil {
		log.Fatalln(err)
	}
}

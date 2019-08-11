package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"path/filepath"

	"github.com/gocarina/gocsv"
	"golang.org/x/text/encoding/japanese"
)

var scnFileFlag = flag.String("scnFiles", "", "scn files")

var jisDecoder = japanese.ShiftJIS.NewDecoder()
var outputFolder = flag.String("outputFolder", "", "output folder")

func Fatal(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func lineStart(i uint32) []byte {
	b := make([]byte, 5)
	b[0] = 0xf3
	binary.LittleEndian.PutUint32(b[1:], i)
	return b
}

func parseJIS(data []byte) string {
	utf8Bytes, err := jisDecoder.Bytes(data)
	if err != nil || len(utf8Bytes) < 2 { // Didn't parse as shift-JIS.
		return ""
	}

	return string(utf8Bytes)
}

type TLLine struct {
	Filename       string `csv:"FILENAME"`
	Index          int    `csv:"INDEX"`
	Length         int    `csv:"LENGTH"`
	OriginalText   string `csv:"ORIGINAL_TEXT"`
	TranslatedText string `csv:"TRANSLATED_TEXT"`
	Notes          string `csv:"NOTES"`
	Status         string `csv:"STATUS"`
	LineStatus     string `csv:"LINE_STATUS"`
}

func main() {
	flag.Parse()

	paths, err := filepath.Glob(*scnFileFlag)
	Fatal(err)
	var tlLines []*TLLine
	for _, path := range paths {
		var i uint32
		data, err := ioutil.ReadFile(path)
		for {
			Fatal(err)
			ls := lineStart(i)
			begin := bytes.Index(data, ls)
			if begin == -1 {
				break
			}
			begin += len(ls)
			length := bytes.IndexByte(data[begin:], 0)
			tlline := &TLLine{Filename: filepath.Base(path), Index: int(i), Length: length, OriginalText: parseJIS(data[begin : begin+length])}
			tlLines = append(tlLines, tlline)
			fmt.Println(tlline)
			i++
		}
	}

	tlLinesCsv, err := gocsv.MarshalBytes(tlLines)
	err = ioutil.WriteFile(filepath.Join(*outputFolder, "tllines.csv"), []byte(tlLinesCsv), 0644)
}

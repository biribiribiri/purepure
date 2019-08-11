package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"io/ioutil"
	"log"
	"path/filepath"

	"github.com/gocarina/gocsv"
	"golang.org/x/text/encoding/japanese"
)

var (
	scnFileFlag     = flag.String("scnFiles", "", "scn files")
	outputFolder    = flag.String("outputFolder", "", "output folder")
	modeFlag        = flag.String("mode", "", "one of: extract, patch")
	translatedCsv   = flag.String("translatedCsv", "", "path to translated csv")
	outputScnFolder = flag.String("outputScnFolder", "", "output folder")

	jisDecoder = japanese.ShiftJIS.NewDecoder()
	jisEncoder = japanese.ShiftJIS.NewEncoder()
)

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

type ScnSegment struct {
	isText    bool
	lineIndex int
	data      []byte
}

func splitFile(data []byte) []*ScnSegment {
	var out []*ScnSegment

	remaining := data
	for i := uint32(0); true; i++ {
		ls := lineStart(i)
		begin := bytes.Index(remaining, ls)
		if begin == -1 {
			// no more data
			out = append(out, &ScnSegment{data: remaining})
			break
		}
		begin += len(ls)
		length := bytes.IndexByte(remaining[begin:], 0)
		if length == -1 {
			log.Fatal("did not find end to line")
		}
		out = append(out, &ScnSegment{data: remaining[:begin]})
		out = append(out, &ScnSegment{isText: true, lineIndex: int(i), data: remaining[begin : begin+length]})
		remaining = remaining[begin+length:]
	}
	return out
}

func combineSegments(segs []*ScnSegment) []byte {
	var out []byte
	for _, s := range segs {
		out = append(out, s.data...)
	}
	return out
}

func fixFileSizeHeader(data []byte, fileSizeOffset uint32) {
	binary.LittleEndian.PutUint32(data, uint32(len(data))-fileSizeOffset)
}

func getFileSizeHeader(data []byte) uint32 {
	return binary.LittleEndian.Uint32(data)
}

func main() {
	flag.Parse()

	switch *modeFlag {
	case "extract":
		paths, err := filepath.Glob(*scnFileFlag)
		Fatal(err)
		var tlLines []*TLLine
		for _, path := range paths {
			data, err := ioutil.ReadFile(path)
			Fatal(err)
			split := splitFile(data)
			for _, ss := range split {
				if !ss.isText {
					continue
				}
				tlline := &TLLine{Filename: filepath.Base(path), Index: ss.lineIndex, Length: len(ss.data), OriginalText: parseJIS(ss.data)}
				tlLines = append(tlLines, tlline)
			}

		}

		tlLinesCsv, err := gocsv.MarshalBytes(tlLines)
		err = ioutil.WriteFile(filepath.Join(*outputFolder, "tllines.csv"), []byte(tlLinesCsv), 0644)
		Fatal(err)
	case "patch":
		var tlLines []*TLLine
		data, err := ioutil.ReadFile(*translatedCsv)
		Fatal(err)
		Fatal(gocsv.UnmarshalBytes(data, &tlLines))

		lineMap := make(map[string]map[int][]byte)
		for _, l := range tlLines {
			if lineMap[l.Filename] == nil {
				lineMap[l.Filename] = make(map[int][]byte)
			}
			if l.TranslatedText == "" {
				continue
			}
			jis, err := jisEncoder.Bytes([]byte(l.TranslatedText))
			Fatal(err)
			lineMap[l.Filename][l.Index] = jis
		}

		paths, err := filepath.Glob(*scnFileFlag)
		Fatal(err)
		for _, path := range paths {
			data, err := ioutil.ReadFile(path)
			Fatal(err)
			origFileSizeHeader := getFileSizeHeader(data)
			fileSizeOffset := uint32(len(data)) - origFileSizeHeader

			base := filepath.Base(path)
			log.Println(base, fileSizeOffset)
			split := splitFile(data)
			for _, ss := range split {
				if !ss.isText {
					continue
				}
				if eng := lineMap[base][ss.lineIndex]; eng != nil {
					ss.data = eng
				}
			}
			outData := combineSegments(split)
			fixFileSizeHeader(outData, fileSizeOffset)
			err = ioutil.WriteFile(filepath.Join(*outputScnFolder, base), outData, 0700)
			Fatal(err)
		}

	default:
		log.Fatalln("invalid mode: ", *modeFlag)
	}
}

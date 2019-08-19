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

var (
	scnFileFlag    = flag.String("scnFiles", "", "scn files")
	engScnFileFlag = flag.String("engScnFiles", "", "scn files")

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

func choiceStart() []byte {
	return []byte{0xf0, 0x1c, 0xf1}
}

func fileTagStart() []byte {
	return []byte{0xf0, 0x1a, 0xf1}
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
	Key            string `csv:"KEY"`
	Length         int    `csv:"LENGTH"`
	OriginalText   string `csv:"ORIGINAL_TEXT"`
	TranslatedText string `csv:"TRANSLATED_TEXT"`
	Notes          string `csv:"NOTES"`
	Status         string `csv:"STATUS"`
	LineStatus     string `csv:"LINE_STATUS"`
}

const (
	TextLineType    string = "text"
	ChoiceLineType  string = "choice"
	FileTagLineType string = "filetag"
)

type ScnSegment struct {
	lineType  string
	lineIndex int
	data      []byte
}

func splitFile(data []byte) []*ScnSegment {
	var out []*ScnSegment

	remaining := data

	indexMap := make(map[string]int)
	for {
		lineType := TextLineType
		ls := lineStart(uint32(indexMap[lineType]))
		begin := bytes.Index(remaining, ls)
		if choiceBegin := bytes.Index(remaining, choiceStart()); choiceBegin != -1 && (begin == -1 || choiceBegin < begin) {
			ls = choiceStart()
			begin = choiceBegin
			lineType = ChoiceLineType
		}
		if fileTagBegin := bytes.Index(remaining, fileTagStart()); fileTagBegin != -1 && (begin == -1 || fileTagBegin < begin) {
			ls = fileTagStart()
			begin = fileTagBegin
			lineType = FileTagLineType
		}

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
		out = append(out, &ScnSegment{lineType: lineType, lineIndex: indexMap[lineType], data: remaining[begin : begin+length]})
		remaining = remaining[begin+length:]
		indexMap[lineType]++
	}

	if !bytes.Equal(data, combineSegments(out)) {
		log.Fatal("splitFile messed up :(")
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

func fixFileSizeHeader(base string, data []byte, fileSizeOffset uint32, segs []*ScnSegment) {
	binary.LittleEndian.PutUint32(data, uint32(len(data))-fileSizeOffset)
	if fileSizeOffset <= 12 {
		return
	}
	numChoices := (fileSizeOffset - 12) / 36

	var pos uint32
	var choicePos []uint32
	for _, ss := range segs {
		if ss.lineType == FileTagLineType {
			choicePos = append(choicePos, pos)
		}
		pos += uint32(len(ss.data))
	}
	if uint32(len(choicePos)) != numChoices {
		log.Printf("WARNING: %v header suggests there should be %v choices, but only found %v in file", base, numChoices, len(choicePos))
		return
	}

	for i := uint32(0); i < numChoices; i++ {
		binary.LittleEndian.PutUint32(data[12+(36*i)+32:], choicePos[i]-fileSizeOffset-uint32(len(fileTagStart())))
	}
}

func getFileSizeHeader(data []byte) uint32 {
	return binary.LittleEndian.Uint32(data)
}

func mapKey(base, lineType string, lineIndex int) string {
	return fmt.Sprintf("%v-%v-%v", base, lineType, lineIndex)
}

func main() {
	flag.Parse()

	switch *modeFlag {
	case "extract":
		lineMap := make(map[string]string)

		if *engScnFileFlag != "" {
			paths, err := filepath.Glob(*engScnFileFlag)
			Fatal(err)
			for _, path := range paths {
				base := filepath.Base(path)
				data, err := ioutil.ReadFile(path)
				Fatal(err)
				split := splitFile(data)
				for _, ss := range split {
					if ss.lineType == "" {
						continue
					}
					lineMap[mapKey(base, ss.lineType, ss.lineIndex)] = parseJIS(ss.data)
				}
			}
		}

		paths, err := filepath.Glob(*scnFileFlag)
		Fatal(err)
		var tlLines []*TLLine
		for _, path := range paths {
			data, err := ioutil.ReadFile(path)
			Fatal(err)
			split := splitFile(data)
			for _, ss := range split {
				if ss.lineType == "" {
					continue
				}
				base := filepath.Base(path)
				tlline := &TLLine{Filename: base, Key: mapKey(base, ss.lineType, ss.lineIndex), Length: len(ss.data), OriginalText: parseJIS(ss.data)}
				tlltext := lineMap[mapKey(base, ss.lineType, ss.lineIndex)]
				if tlltext != "" && tlltext != tlline.OriginalText {
					tlline.TranslatedText = tlltext
				}
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

		lineMap := make(map[string][]byte)
		for _, l := range tlLines {
			if l.TranslatedText == "" || l.Key == "" {
				continue
			}
			jis, err := jisEncoder.Bytes([]byte(l.TranslatedText))
			Fatal(err)
			lineMap[l.Key] = jis
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
				if ss.lineType == "" {
					continue
				}
				if eng := lineMap[mapKey(base, ss.lineType, ss.lineIndex)]; eng != nil {
					ss.data = eng
				}
			}
			outData := combineSegments(split)
			fixFileSizeHeader(base, outData, fileSizeOffset, split)
			err = ioutil.WriteFile(filepath.Join(*outputScnFolder, base), outData, 0700)
			Fatal(err)
		}

	default:
		log.Fatalln("invalid mode: ", *modeFlag)
	}
}

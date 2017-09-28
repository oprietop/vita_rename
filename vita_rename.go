package main

import (
    "archive/zip"
    "bytes"
    "encoding/binary"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "strings"
    "sync"
)

// http://www.vitadevwiki.com/index.php?title=System_File_Object_(SFO)_(PSF)#Header_SFO
type Header struct {
    Magic      int32 //   0:4 1179865088 int32 little endian = 00505346 hex
    Version    int32 //   4:8
    KeyOffset  int32 //  8:12
    DataOffset int32 // 12:16
    Entries    int32 // 16:20
}

// http://www.vitadevwiki.com/index.php?title=System_File_Object_(SFO)_(PSF)#Index_table
type Index struct {
    KeyOffset       int16 // 0x02
    ParamFmt        int16 // 0x02
    ParamLength     int32 // 0x04
    ParamMaxLength  int32 // 0x04
    DataTableOffset int32 // 0x04
}

// struct to store the info we will use to name the file
type NameData struct {
    title, appVer, version, titleId, region string
    ac                                      int
}

// region code correlation from http://www.edepot.com/playstation.html and others
var regions = map[string]string{
    "PCSB": "EUR", "VCES": "EUR", "VLES": "EUR", "PCSF": "EUR",
    "PCSE": "USA", "PCSA": "USA", "PCSD": "USA", "VCUS": "USA", "VLUS": "USA",
    "PCSG": "JAP", "PCSC": "JAP", "VCJS": "JAP", "VLJM": "JAP", "VLJS": "JAP",
    "PCSH": "ASIA", "VCAS": "ASIA", "VLAS": "ASIA",
}

// common error validator
func check(e error) {
    if e != nil && e != io.EOF {
        panic(e)
    }
}

// sanitize the destination name
func safeString(s string) string {
    r := strings.NewReplacer(
        "\000", "",
        "\r", "",
        "\n", "",
        "\\", "",
        "\"", "",
        "/", "",
        ":", "",
        "*", "",
        "?", "",
        "<", "",
        ">", "",
        "|", "",
    )

    return r.Replace(s)
}

// return a map with the info from a valid SFO contents
func parseSfo(sfob []byte) map[string]string {
    // map to store the items with a default region
    m := map[string]string{"REGION": "UNK"}
    h := Header{}
    // need a buffer to seek via binary.Read
    buffer := bytes.NewBuffer(sfob)
    // match our SFO header
    err := binary.Read(buffer, binary.LittleEndian, &h)
    check(err)
    if len(os.Args[1:]) > 0 {
        fmt.Printf("HEADER: %+v\n", h)
    }
    // Finish if we don't have valid SFO magic
    if h.Magic != 1179865088 {
        return m
    }
    // Get a single element slice with the keys
    slice := sfob[h.KeyOffset:h.DataOffset]
    // Trim nulls before splitting
    slice = bytes.Trim(slice, "\x00")
    // Split the slice again, this time to get all the element
    keys := bytes.Split(slice, []byte("\x00"))
    // iterate over the keys slice
    for in, k := range keys {
        i := Index{}
        // seek the buffer for the Index table
        err = binary.Read(buffer, binary.LittleEndian, &i)
        check(err)
        start := h.DataOffset + i.DataTableOffset
        end := start + i.ParamLength
        // ge a slice with the value
        data_slice := sfob[start:end]
        // stringify and sanitize the key/value and store it on a map
        k := fmt.Sprintf("%s", k)
        v := fmt.Sprintf("%s", data_slice)
        m[k] = safeString(v)
        if len(os.Args[1:]) > 0 {
            fmt.Printf("[%d] (%d-%d) '%s' -> '%s'\n", in, start, end, k, m[k])
        }
        // generate a custom REGION key with TITLE_ID
        if tid, ok := m["TITLE_ID"]; ok {
            regCode := tid[0:4]
            if reg, ok := regions[regCode]; ok {
                m["REGION"] = reg
            }
        }
    }

    return m
}

// process a zip file
func task(file string) {
    // open the file
    r, err := zip.OpenReader(file)
    check(err)
    if len(os.Args[1:]) > 0 {
        fmt.Printf("File: '%s'\n", file)
    }
    // init vars
    data := NameData{}
    // cycle through zip ms
    for _, m := range r.File {
        // do stuff if we go a param.sfo file
        if strings.HasSuffix(m.Name, "param.sfo") {
            if len(os.Args[1:]) > 0 {
                fmt.Printf("SFO: '%s' with %d bytes", m.Name, m.UncompressedSize)
            }
            // open the file
            rc, err := m.Open()
            check(err)
            // Will only take the first MB of the file
            sfob := make([]byte, 10000000)
            s, err := rc.Read(sfob)
            check(err)
            // close the handle
            rc.Close()
            if len(os.Args[1:]) > 0 {
                fmt.Printf(", got %d bytes.\n", s)
            }
            // process the file contents
            m := parseSfo(sfob)
            if m["CATEGORY"] == "ac" {
                data.ac++
            }
            // valid SFOs to take into account for naming are the ones with an APP_VER key
            if _, ok := m["APP_VER"]; ok {
                // update variables, we want to know the higher version
                if m["APP_VER"] > data.appVer {
                    data.appVer = m["APP_VER"]
                }
                if m["VERSION"] > data.version {
                    data.version = m["VERSION"]
                }
                // keep the info we want:w
                data.title, data.titleId, data.region = m["TITLE"], m["TITLE_ID"], m["REGION"]
            }
        }
    }
    // we're done with this file, close the reader
    r.Close()
    // Rename the zip file if we got new info
    if data.title != "" {
        // generate a newName candidate
        newName := fmt.Sprintf("%s (%s-%s-%d) [%s] (%s).zip", data.title, data.appVer, data.version, data.ac, data.titleId, data.region)
        // Check if our target file does not exists
        if _, err := os.Stat(newName); os.IsNotExist(err) {
            // rename
            err := os.Rename(file, newName)
            check(err)
            fmt.Printf("Moving '\033[36m%s\033[39m' to '\033[33m%s\033[39m': \033[32mOK!\033[39m\n", file, newName)
        } else {
            fmt.Printf("Moving '\033[36m%s\033[39m' to '\033[33m%s\033[39m': \033[31mFile Exists!\033[39m\n", file, newName)
        }
    }
}

func main() {
    // create a WaitGroup
    var wg sync.WaitGroup
    // get all the zip files in the path
    files, _ := filepath.Glob("*.zip")
    // iterate the files
    for _, file := range files {
        // IMPORTANT: variables declared within the body of a loop are not shared between iterations
        f := file
        // go routine launching an anonymous function to wrap the stuff we want to do
        go func() {
            // Increment wg
            wg.Add(1)
            // decrement wg when the function exists
            defer wg.Done()
            // The actual function we want to run
            task(f)
        }()
    }
    // wait for all the go routines to finish (wg becoming 0)
    wg.Wait()
}

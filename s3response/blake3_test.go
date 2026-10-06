package s3response

import (
	"encoding/xml"
	"strings"
	"testing"
)

// TestBlake3TreeXML pins the element shape the Blake3 object attribute
// documents: CID, ChunkLog and the base64 Outboard.
func TestBlake3TreeXML(t *testing.T) {
	res := GetObjectAttributesResponse{
		Blake3: &Blake3Tree{CID: "bafkr4iexample", ChunkLog: 12, Outboard: "AAAAAAAAAAA="},
	}
	out, err := xml.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	want := "<Blake3><CID>bafkr4iexample</CID><ChunkLog>12</ChunkLog><Outboard>AAAAAAAAAAA=</Outboard></Blake3>"
	if !strings.Contains(string(out), want) {
		t.Fatalf("marshaled %s, want it to contain %s", out, want)
	}
	if out, _ := xml.Marshal(GetObjectAttributesResponse{}); strings.Contains(string(out), "Blake3") {
		t.Fatalf("a nil tree must omit the element: %s", out)
	}
}

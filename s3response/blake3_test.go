package s3response

import (
	"encoding/xml"
	"strings"
	"testing"
)

// TestBlake3TreeXML pins the element shape the Blake3 object attribute
// documents: CID, Group, and one Leaf per block under Leaves.
func TestBlake3TreeXML(t *testing.T) {
	res := GetObjectAttributesResponse{
		Blake3: &Blake3Tree{CID: "bafkr4iexample", Group: 22, Leaves: []string{"AAEC", "AwQF"}},
	}
	out, err := xml.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	want := "<Blake3><CID>bafkr4iexample</CID><Group>22</Group><Leaves><Leaf>AAEC</Leaf><Leaf>AwQF</Leaf></Leaves></Blake3>"
	if !strings.Contains(string(out), want) {
		t.Fatalf("marshaled %s, want it to contain %s", out, want)
	}
	if out, _ := xml.Marshal(GetObjectAttributesResponse{}); strings.Contains(string(out), "Blake3") {
		t.Fatalf("a nil tree must omit the element: %s", out)
	}
}

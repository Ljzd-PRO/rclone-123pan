package api

import (
	"encoding/json"
	"testing"
)

func TestCopyRequestJSONMatchesOfficialWebShape(t *testing.T) {
	request := CopyRequest{
		FileList: []CopyFile{{
			FileID: 1, Size: 2, ETag: "etag", Type: 0, ParentFileID: 3, FileName: "name", DriveID: 0,
		}},
		TargetFileID: 4,
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"fileList":[{"fileId":1,"size":2,"etag":"etag","type":0,"parentFileId":3,"fileName":"name","driveId":0}],"targetFileId":4}`
	if string(body) != want {
		t.Fatalf("Copy request JSON\n got: %s\nwant: %s", body, want)
	}
}

func TestCopyResponsesRequireExplicitStatusFields(t *testing.T) {
	var start CopyStartData
	if err := json.Unmarshal([]byte(`{"taskId":1}`), &start); err != nil {
		t.Fatal(err)
	}
	if start.Mode != nil {
		t.Fatal("missing mode was not preserved")
	}
	var task CopyTaskData
	if err := json.Unmarshal([]byte(`{"reason":"missing"}`), &task); err != nil {
		t.Fatal(err)
	}
	if task.Status != nil {
		t.Fatal("missing status was not preserved")
	}
}

package persistence

import (
	"fmt"
	"testing"

	"agentworkflow/workflow/dsl"
)

func TestVProbe2(t *testing.T) {
	db := openTestDB(t)
	doc, _ := dsl.Parse([]byte(demoYAML))
	wf := doc.ToModel()
	if err := db.SaveWorkflow(wf, false); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetWorkflow("wf-demo")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("got.Version =", got.Version)
	got.Description = "updated"
	err = db.SaveWorkflow(got, true)
	fmt.Println("save v2 err =", err, " got.Version now =", got.Version)
	vers, _ := db.ListWorkflowVersions("wf-demo")
	fmt.Println("versions =", vers)
}

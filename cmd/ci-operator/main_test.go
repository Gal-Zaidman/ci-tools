package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/pod-utils/downwardapi"

	"github.com/google/go-cmp/cmp"
	"github.com/openshift/ci-tools/pkg/api"
)

func TestSanitizeMessage(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		expected string
	}{{
		name:     "pod name",
		message:  "...pod ci-op-4fg72pn0/unit...",
		expected: "...pod <PODNAME>/unit...",
	}, {
		name:     "ci-operator duration seconds",
		message:  "...after 39s (failed...",
		expected: "...after <DURATION> (failed...",
	}, {
		name:     "seconds-like pattern not replaced inside words",
		message:  "some hash is 'h4sh'",
		expected: "some hash is 'h4sh'",
	}, {
		name:     "ci-operator duration minutes",
		message:  "...after 1m39s (failed...",
		expected: "...after <DURATION> (failed...",
	}, {
		name:     "ci-operator duration hours",
		message:  "...after 69h1m39s (failed...",
		expected: "...after <DURATION> (failed...",
	}, {
		name:     "seconds duration",
		message:  "...PASS: TestRegistryProviderGet (2.83s)...",
		expected: "...PASS: TestRegistryProviderGet (<DURATION>)...",
	}, {
		name:     "ms duration",
		message:  "...PASS: TestRegistryProviderGet 510ms...",
		expected: "...PASS: TestRegistryProviderGet <DURATION>...",
	}, {
		name:     "spaced duration",
		message:  "...exited with code 1 after 00h 17m 40s...",
		expected: "...exited with code 1 after <DURATION>...",
	}, {
		name:     "ISO time",
		message:  "...time=\"2019-05-21T15:31:35Z\"...",
		expected: "...time=\"<ISO-DATETIME>\"...",
	}, {
		name:     "ISO DATE",
		message:  "...date=\"2019-05-21\"...",
		expected: "...date=\"<ISO-DATETIME>\"...",
	}, {
		name:     "UUID",
		message:  "...UUID:\"8f4e0db5-86a8-11e9-8c0a-12bbdc8a555a\"...",
		expected: "...UUID:\"<UUID>\"...",
	},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeMessage(tc.message); got != tc.expected {
				t.Errorf("sanitizeMessage('%s') = '%s', expected '%s'", tc.message, got, tc.expected)
			}
		})
	}
}

func TestProwMetadata(t *testing.T) {
	tests := []struct {
		TestName       string
		Org            string
		Repo           string
		ExtraOrg       string
		ExtraRepo      string
		ProwJobID      string
		Namespace      string
		CustomMetadata map[string]string
	}{
		{
			TestName:  "generate metadata",
			Org:       "some-org",
			Repo:      "some-repo",
			ExtraOrg:  "some-extra-org",
			ExtraRepo: "some-extra-repo",
			ProwJobID: "some-prow-job-id",
			Namespace: "some-namespace",
		},
		{
			TestName:  "generate metadata with a custom metadata file",
			Org:       "some-org",
			Repo:      "some-repo",
			ExtraOrg:  "some-extra-org",
			ExtraRepo: "some-extra-repo",
			ProwJobID: "some-prow-job-id",
			Namespace: "some-namespace",
			CustomMetadata: map[string]string{
				"custom-field1": "custom-value1",
				"custom-field2": "custom-value2",
			},
		},
	}
	for _, tc := range tests {
		err := verifyMetadata(tc.Org, tc.Repo, tc.ExtraOrg, tc.ExtraRepo, tc.ProwJobID, tc.Namespace, tc.CustomMetadata)
		if err != nil {
			t.Errorf("Test case '%s': error while running test: %v", tc.TestName, err)
		}
	}
}

func verifyMetadata(org string,
	repo string,
	extraOrg string,
	extraRepo string,
	prowJobID string,
	namespace string,
	customMetadata map[string]string) error {
	tempDir, err := ioutil.TempDir("", "")
	if err != nil {
		return fmt.Errorf("Unable to create temporary directory: %v", err)
	}

	defer os.RemoveAll(tempDir)

	metadataFile := filepath.Join(tempDir, "metadata.json")

	// Verify without custom metadata
	o := &options{
		artifactDir: tempDir,
		jobSpec: &api.JobSpec{
			JobSpec: downwardapi.JobSpec{
				Refs:      &prowapi.Refs{Org: org, Repo: repo},
				ExtraRefs: []prowapi.Refs{{Org: extraOrg, Repo: extraRepo}},
				ProwJobID: prowJobID,
			},
		},
		namespace: namespace,
	}
	err = o.writeMetadataJSON()
	if err != nil {
		return fmt.Errorf("error while writing metadata JSON: %v", err)
	}

	metadataFileContents, err := ioutil.ReadFile(metadataFile)
	if err != nil {
		return fmt.Errorf("error reading metadata file: %v", err)
	}

	var writtenMetadata prowResultMetadata
	err = json.Unmarshal(metadataFileContents, &writtenMetadata)
	if err != nil {
		return fmt.Errorf("error parsing prow metadata: %v", err)
	}

	expectedMetadata := prowResultMetadata{
		Revision:   1,
		RepoCommit: "",
		Repo:       fmt.Sprintf("%s/%s", org, repo),
		Repos: map[string]string{
			fmt.Sprintf("%s/%s", extraOrg, extraRepo): "",
			fmt.Sprintf("%s/%s", org, repo):           "",
		},
		InfraCommit:   "",
		JobVersion:    "",
		Pod:           prowJobID,
		WorkNamespace: namespace,
		Metadata:      nil}
	if !reflect.DeepEqual(expectedMetadata, writtenMetadata) {
		return fmt.Errorf("written metadata does not match expected metadata: %s", cmp.Diff(expectedMetadata, writtenMetadata))
	}

	testArtifactDirectory := filepath.Join(tempDir, "test-artifact-directory")
	if os.Mkdir(testArtifactDirectory, os.FileMode(0755)) != nil {
		return fmt.Errorf("unable to create artifact directory under temporary directory")
	}

	if len(customMetadata) > 0 {
		testJSON, err := json.MarshalIndent(customMetadata, "", "")
		if err != nil {
			return fmt.Errorf("error marshalling custom metadata: %v", err)
		}
		err = ioutil.WriteFile(filepath.Join(testArtifactDirectory, "custom-prow-metadata.json"), []byte(testJSON), os.FileMode(0644))
		if err != nil {
			return fmt.Errorf("unable to create custom metadata file: %v", err)
		}
	}

	// Write a bunch of empty files that should be ignored
	err = ioutil.WriteFile(filepath.Join(testArtifactDirectory, "a-ignore1.txt"), []byte(``), os.FileMode(0644))
	err = ioutil.WriteFile(filepath.Join(testArtifactDirectory, "b-ignore1.txt"), []byte(`{"invalid-field1": "invalid-value1"}`), os.FileMode(0644))
	err = ioutil.WriteFile(filepath.Join(testArtifactDirectory, "d-ignore1.txt"), []byte(``), os.FileMode(0644))
	err = ioutil.WriteFile(filepath.Join(testArtifactDirectory, "e-ignore1.txt"), []byte(`{"invalid-field2": "invalid-value2"}`), os.FileMode(0644))
	if err != nil {
		return fmt.Errorf("one or more of the empty *ignore files failed to write: %v", err)
	}

	err = o.writeMetadataJSON()
	if err != nil {
		return fmt.Errorf("error while writing metadata JSON: %v", err)
	}

	metadataFileContents, err = ioutil.ReadFile(metadataFile)
	if err != nil {
		return fmt.Errorf("error reading metadata file (second revision): %v", err)
	}

	err = json.Unmarshal(metadataFileContents, &writtenMetadata)
	if err != nil {
		return fmt.Errorf("error parsing prow metadata (second revision): %v", err)
	}

	hasCustomMetadata := len(customMetadata) > 0
	revision := 1
	if hasCustomMetadata {
		revision = 2
	}

	expectedMetadata.Revision = revision
	expectedMetadata.Metadata = customMetadata
	if !reflect.DeepEqual(expectedMetadata, writtenMetadata) {
		return fmt.Errorf("written metadata does not match expected metadata (second revision): %s", cmp.Diff(expectedMetadata, writtenMetadata))
	}

	return nil
}

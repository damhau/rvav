package vra

import (
	"encoding/json"
	"strings"
	"testing"
)

// The IaaS API's shapes, as its reference gives them: a project's
// customProperties, a profile's region by its link. Estates stored before
// T1.7 are still read, and what rvav writes reads back.
func TestIaaSShapes(t *testing.T) {
	var p Project
	if err := json.Unmarshal([]byte(`{"id": "p1", "name": "web", "customProperties": {"costCenter": "CC-1"}}`), &p); err != nil ||
		p.Properties["costCenter"] != "CC-1" {
		t.Fatalf("project = %+v, %v", p, err)
	}
	var old Project
	if err := json.Unmarshal([]byte(`{"id": "p1", "properties": {"costCenter": "CC-0"}}`), &old); err != nil || old.Properties["costCenter"] != "CC-0" {
		t.Fatalf("a stored project = %+v, %v", old, err)
	}

	var ip ImageProfile
	vra := `{"id": "ip1", "name": "gva", "externalRegionId": "Datacenter:datacenter-3",
		"imageMappings": {"mapping": {"rhel9": {"name": "tpl-rhel9", "cloudConfig": "#cloud-config"}}},
		"_links": {"region": {"href": "/iaas/api/regions/r-gva"}, "self": {"href": "/iaas/api/image-profiles/ip1"}}}`
	if err := json.Unmarshal([]byte(vra), &ip); err != nil || ip.RegionID != "r-gva" || ip.ImageMappings.Mapping["rhel9"].Name != "tpl-rhel9" {
		t.Fatalf("image profile = %+v, %v", ip, err)
	}
	var fp FlavorProfile
	if err := json.Unmarshal([]byte(`{"id": "fp1", "flavorMappings": {"mapping": {"small": {"cpuCount": 2, "memoryInMB": 4096}}},
		"_links": {"region": {"href": "/iaas/api/regions/r-gva"}}}`), &fp); err != nil || fp.RegionID != "r-gva" || fp.FlavorMappings.Mapping["small"].CPUCount != 2 {
		t.Fatalf("flavor profile = %+v, %v", fp, err)
	}
	var stored ImageProfile
	if err := json.Unmarshal([]byte(`{"id": "ip1", "regionId": "r-old", "imageMappings": {"mapping": {}}}`), &stored); err != nil || stored.RegionID != "r-old" {
		t.Fatalf("a stored image profile = %+v, %v", stored, err)
	}

	b, err := json.Marshal(Estate{Projects: []Project{p}, Images: []ImageProfile{ip}, Flavors: []FlavorProfile{fp}})
	if err != nil || strings.Contains(string(b), "regionId") || strings.Contains(string(b), `"properties"`) ||
		!strings.Contains(string(b), `"href":"/iaas/api/regions/r-gva"`) || !strings.Contains(string(b), `"customProperties"`) {
		t.Fatalf("written = %s, %v", b, err)
	}
	var back Estate
	if err := json.Unmarshal(b, &back); err != nil || back.Images[0].RegionID != "r-gva" || back.Flavors[0].RegionID != "r-gva" ||
		back.Projects[0].Properties["costCenter"] != "CC-1" {
		t.Fatalf("read back = %+v, %v", back, err)
	}
}

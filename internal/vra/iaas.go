package vra

import (
	"cmp"
	"encoding/json"
	"path"
)

// The IaaS API names a project's custom properties customProperties, and a
// profile's region by a link (_links.region.href, /iaas/api/regions/<id>):
// its responses have no regionId. Estates stored before T1.7 (#140) used
// properties and regionId, so both are read.

// regionLink is a profile's _links, of which rvav reads the region.
type regionLink struct {
	Region *href `json:"region,omitempty"`
}

type href struct {
	Href string `json:"href"`
}

func (l regionLink) id() string {
	if l.Region == nil || l.Region.Href == "" {
		return ""
	}
	return path.Base(l.Region.Href)
}

func linkTo(region string) *regionLink {
	if region == "" {
		return nil
	}
	return &regionLink{Region: &href{"/iaas/api/regions/" + region}}
}

func (p *Project) UnmarshalJSON(b []byte) error {
	type plain Project
	var v struct {
		plain
		Stored map[string]string `json:"properties"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*p = Project(v.plain)
	if p.Properties == nil {
		p.Properties = v.Stored
	}
	return nil
}

func (p ImageProfile) MarshalJSON() ([]byte, error) {
	type plain ImageProfile
	return json.Marshal(struct {
		plain
		RegionID string      `json:"regionId,omitempty"`
		Links    *regionLink `json:"_links,omitempty"`
	}{plain: plain(p), Links: linkTo(p.RegionID)})
}

func (p *ImageProfile) UnmarshalJSON(b []byte) error {
	type plain ImageProfile
	var v struct {
		plain
		Links regionLink `json:"_links"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*p = ImageProfile(v.plain)
	p.RegionID = cmp.Or(v.Links.id(), p.RegionID)
	return nil
}

func (p FlavorProfile) MarshalJSON() ([]byte, error) {
	type plain FlavorProfile
	return json.Marshal(struct {
		plain
		RegionID string      `json:"regionId,omitempty"`
		Links    *regionLink `json:"_links,omitempty"`
	}{plain: plain(p), Links: linkTo(p.RegionID)})
}

func (p *FlavorProfile) UnmarshalJSON(b []byte) error {
	type plain FlavorProfile
	var v struct {
		plain
		Links regionLink `json:"_links"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*p = FlavorProfile(v.plain)
	p.RegionID = cmp.Or(v.Links.id(), p.RegionID)
	return nil
}

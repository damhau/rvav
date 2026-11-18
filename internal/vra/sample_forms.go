package vra

// SampleFieldTypes is a released cloud template of Web Platform with an
// input of each type and its custom form, which shows them with each of
// vRA's widgets: for the importer's tests, apart from SampleEstate.
func SampleFieldTypes() (Blueprint, Form) {
	bp := Blueprint{ID: "bp-types", Name: "Field types", ProjectID: "vra-prj-web", Status: "RELEASED", Content: fieldTypesTemplate,
		Versions: []BlueprintVersion{{ID: "bp-types-v1", BlueprintID: "bp-types", Version: "1", Status: "RELEASED", Content: fieldTypesTemplate}}}
	form := Form{ID: "form-types", Name: "Field types", SourceID: "bp-types", SourceType: "com.vmw.blueprint", Type: "requestForm",
		Status: "ON", Form: fieldTypesForm}
	return bp, form
}

const fieldTypesTemplate = `formatVersion: 1
inputs:
  environment:
    type: string
    enum: [dev, prd]
  window:
    type: string
    title: Maintenance window
  ratio:
    type: number
    minimum: 0.5
    maximum: 4
  adminPassword:
    type: string
    encrypted: true
    default: changeme
  apiToken:
    type: string
    default: tok-123
  labels:
    type: array
    items:
      type: string
  features:
    type: array
    items:
      type: string
  owners:
    type: array
    items:
      type: string
  disks:
    type: array
    minItems: 1
    maxItems: 4
    items:
      type: object
      required: [size]
      properties:
        size:
          type: integer
          minimum: 10
        label:
          type: string
        tier:
          type: string
          enum: [gold, silver]
        thin:
          type: boolean
  ports:
    type: array
    items:
      type: integer
  meta:
    type: object
  catalog:
    type: string
  backup:
    type: object
    required: [policy]
    properties:
      policy:
        type: string
        enum: [daily, weekly]
      keepDays:
        type: integer
        minimum: 1
  teams:
    type: array
    items:
      type: string
  zones:
    type: array
    items:
      type: string
resources:
  Cloud_vSphere_Machine_1:
    type: Cloud.vSphere.Machine
    properties:
      image: rhel9
      flavor: medium
`

const fieldTypesForm = `{"layout":{"pages":[{"id":"page_general","title":"Server","sections":[
{"id":"section_main","fields":[
{"id":"environment","display":"dropDown","state":{"visible":true,"read-only":false}},
{"id":"window","display":"datetime","state":{"visible":true,"read-only":false}},
{"id":"ratio","display":"decimalField","state":{"visible":true,"read-only":false}},
{"id":"adminPassword","display":"passwordField","state":{"visible":true,"read-only":false}},
{"id":"apiToken","display":"passwordField","state":{"visible":true,"read-only":false}},
{"id":"labels","display":"tagField","state":{"visible":true,"read-only":false}},
{"id":"features","display":"multiSelect","state":{"visible":true,"read-only":false}},
{"id":"owners","display":"dualList","state":{"visible":true,"read-only":false}},
{"id":"disks","display":"datagrid","state":{"visible":true,"read-only":false}},
{"id":"catalog","display":"valuePicker","state":{"visible":true,"read-only":false}},
{"id":"backup","display":"objectField","state":{"visible":true,"read-only":false}},
{"id":"teams","display":"dualList","state":{"visible":true,"read-only":false}},
{"id":"zones","display":"multiValuePicker","state":{"visible":true,"read-only":false}},
{"id":"text_notice","display":"text","state":{"visible":true,"read-only":false}},
{"id":"image_zones","display":"image","state":{"visible":true,"read-only":false}},
{"id":"image_logo","display":"image","state":{"visible":true,"read-only":false}},
{"id":"link_sizes","display":"link","state":{"visible":true,"read-only":false}}]}]}]},
"schema":{
"environment":{"label":"Environment","type":{"dataType":"string","isMultiple":false},"valueList":[{"label":"Development","value":"dev"},{"label":"Production","value":"prd"}],"constraints":{"required":true}},
"window":{"label":"Maintenance window","type":{"dataType":"dateTime","isMultiple":false}},
"ratio":{"label":"CPU ratio","type":{"dataType":"decimal","isMultiple":false},"constraints":{"min-value":0.5,"max-value":4}},
"adminPassword":{"label":"Admin password","type":{"dataType":"secureString","isMultiple":false},"default":"changeme"},
"apiToken":{"label":"API token","type":{"dataType":"secureString","isMultiple":false},"default":"tok-123"},
"labels":{"label":"Labels","type":{"dataType":"string","isMultiple":true},"valueList":[{"label":"web","value":"web"},{"label":"pci","value":"pci"}]},
"features":{"label":"Features","type":{"dataType":"string","isMultiple":true},"valueList":[{"label":"Back up","value":"backup"},{"label":"Monitoring","value":"monitoring"}],"default":["backup"]},
"owners":{"label":"Owners","type":{"dataType":"string","isMultiple":true},"valueList":{"id":"com.corp.ad/getUsers","type":"scriptAction","parameters":[]}},
"disks":{"label":"Disks","type":{"dataType":"complex","isMultiple":true,"fields":[{"id":"size","label":"Size (GB)","type":{"dataType":"integer"}},{"id":"tier","label":"Tier","type":{"dataType":"string"}}]},"default":[{"size":20,"tier":"gold"}]},
"catalog":{"label":"Catalog","type":{"dataType":"string","isMultiple":false},"valueList":{"id":"com.corp.cmdb/search","type":"scriptAction","parameters":[]}},
"backup":{"label":"Backup","type":{"dataType":"complex","isMultiple":false,"fields":[{"id":"policy","label":"Policy","type":{"dataType":"string"}},{"id":"keepDays","label":"Keep (days)","type":{"dataType":"integer"}}]}},
"teams":{"label":"Teams","type":{"dataType":"string","isMultiple":true},"valueList":[{"label":"Web team","value":"web"},{"label":"Network team","value":"net"}]},
"zones":{"label":"Zones","type":{"dataType":"string","isMultiple":true},"valueList":[{"label":"DMZ","value":"dmz"},{"label":"LAN","value":"lan"}]},
"text_notice":{"label":"Text","type":{"dataType":"string","isMultiple":false},"default":"Ask the network team before you open a port."},
"image_zones":{"label":"Network zones","type":{"dataType":"string","isMultiple":false},"default":"https://wiki.corp.example/img/zones.png"},
"image_logo":{"label":"Logo","type":{"dataType":"string","isMultiple":false},"default":"data:image/png;base64,iVBORw0KGgo="},
"link_sizes":{"label":"VM sizes explained","type":{"dataType":"string","isMultiple":false},"default":"https://wiki.corp.example/vm-sizes"}},
"options":{"externalValidations":[]}}`

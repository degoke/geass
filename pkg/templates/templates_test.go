package templates

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/degoke/geass/pkg/platform"
)

func TestEveryBuiltinTemplateIsValid(t *testing.T) {
	list := Builtin()
	require.NotEmpty(t, list)
	ids := map[string]bool{}
	for _, template := range list {
		require.NoError(t, template.Validate(), template.ID)
		require.True(t, template.Builtin, template.ID)
		require.False(t, ids[template.ID], "duplicate id %s", template.ID)
		ids[template.ID] = true
	}
	found, ok := Find("infisical")
	require.True(t, ok)
	require.Equal(t, "Infisical", found.Name)
	_, ok = Find("missing")
	require.False(t, ok)
}

// The old starter examples moved to the documentation. They must not come back as defaults.
func TestTheCatalogHoldsNoStarterExamples(t *testing.T) {
	for _, id := range []string{"hello-function", "echo-demo", "postgres-adminer", "function-postgres", "hasura-postgres"} {
		_, ok := Find(id)
		require.False(t, ok, id)
	}
}

// A catalog entry is a real product: a person has to be able to run it from what the entry says.
func TestCatalogEntriesCarryWhatTheyNeedToRun(t *testing.T) {
	gatus, ok := Find("gatus")
	require.True(t, ok)
	require.NotEmpty(t, gatus.Notes)
	require.Contains(t, gatus.Resources[0].Files["config.yaml"], "endpoints:", "Gatus reads its endpoints from this file")
	require.Equal(t, "/config/config.yaml", gatus.Resources[0].Variables[0].Value)

	infisical, ok := Find("infisical")
	require.True(t, ok)
	require.NotEmpty(t, infisical.Notes)
	var service Resource
	for _, resource := range infisical.Resources {
		if resource.Kind == KindService {
			service = resource
		}
	}
	require.NotNil(t, service.Size, "Infisical does not fit the default 128 MB")
	resources := service.AppResources()
	require.Equal(t, "1Gi", resources.Requests.Memory().String(), "it idles near 720 MiB")
	require.Equal(t, "2Gi", resources.Limits.Memory().String(), "its first start peaks near 960 MiB")
	generated := map[string]bool{}
	for _, variable := range service.Variables {
		if variable.Generate == GenerateSecretKind {
			generated[variable.Name] = true
		}
	}
	require.True(t, generated["ENCRYPTION_KEY"] && generated["AUTH_SECRET"])
}

// A catalog entry is tested with the image it names, so the image has to be one version, not whatever is newest.
func TestEveryCatalogImageIsPinnedToAVersion(t *testing.T) {
	for _, template := range Builtin() {
		for _, resource := range template.Resources {
			if resource.Image == "" {
				continue
			}
			_, tag, found := strings.Cut(resource.Image[strings.LastIndex(resource.Image, "/")+1:], ":")
			require.True(t, found, "%s: %s has no tag", template.ID, resource.Image)
			require.NotEqual(t, "latest", tag, "%s: %s", template.ID, resource.Image)
		}
	}
}

// ReFHIR 0.0.2 starts from FHIR packages prepared inside the image and peaked near 120 MiB, where 0.0.1
// peaked near 3.1 GB. The catalog reserves a small amount and leaves room above it for more data.
func TestReFHIRReservesLittleAndMayUseHeadroomAboveIt(t *testing.T) {
	refhir, ok := Find("refhir")
	require.True(t, ok)
	require.NotEmpty(t, refhir.Notes)
	require.Contains(t, refhir.Notes, "REFHIR_PACKAGE_DOWNLOAD", "downloads are off by default, so installing a package answers 403, and the note says how to allow it")
	var service Resource
	for _, resource := range refhir.Resources {
		if resource.Kind == KindService {
			service = resource
		}
	}
	require.Equal(t, "ghcr.io/hai-hq/refhir:0.0.2", service.Image)
	require.Equal(t, int32(8080), service.Port)

	resources := service.AppResources()
	require.Equal(t, "256Mi", resources.Requests.Memory().String())
	require.Equal(t, "1Gi", resources.Limits.Memory().String(), "about 8 times the busiest peak measured (107 to 122 MiB) and 4 times the reserve")
	require.Equal(t, "250m", resources.Requests.Cpu().String())
	require.Equal(t, "1", resources.Limits.Cpu().String())

	variables := map[string]Variable{}
	for _, variable := range service.Variables {
		variables[variable.Name] = variable
	}
	require.Equal(t, &Reference{Resource: databaseKey, Key: "uri"}, variables["REFHIR_POSTGRES_URL"].From)
	require.Equal(t, GenerateSecretKind, variables["REFHIR_AUDIT_KEY"].Generate, "the audit key has to be set, and Geass makes one")
	// The entry starts from the packages bundled in the image. Allowing downloads, or switching the bundle off,
	// would bring back the first-start network dependency and the memory it cost.
	require.NotContains(t, variables, "REFHIR_PACKAGE_DOWNLOAD")
	require.NotContains(t, variables, "REFHIR_PACKAGE_BUNDLE")
	require.Contains(t, refhir.Summary(), "1 database")
}

func TestBrowserbayReservesWhatItRunsOnAndMayUseWhatBusyPagesNeed(t *testing.T) {
	browserbay, ok := Find("browserbay")
	require.True(t, ok)
	require.NotEmpty(t, browserbay.Notes)
	require.Len(t, browserbay.Resources, 1, "the relay and a Chromium node are one process")
	service := browserbay.Resources[0]
	require.Equal(t, "ghcr.io/degoke/browserbay:0.2.2", service.Image)
	require.Equal(t, int32(8788), service.Port)

	resources := service.AppResources()
	require.Equal(t, "1Gi", resources.Requests.Memory().String())
	require.Equal(t, "4Gi", resources.Limits.Memory().String(), "five busy pages peaked near 1.5 GB, and a 1 GB limit made a page time out")
	require.Equal(t, GenerateSecretKind, service.Variables[0].Generate)
	require.Equal(t, "BROWSERBAY_API_KEY", service.Variables[0].Name)
}

// Databases and object storage never burst, and a catalog database carries no size at all.
func TestNoCatalogDatabaseHasASize(t *testing.T) {
	for _, template := range Builtin() {
		for _, resource := range template.Resources {
			if resource.Kind == KindDatabase {
				require.Nil(t, resource.Size, "%s/%s", template.ID, resource.Key)
			}
		}
	}
}

func valid() Template {
	return Template{
		ID: "demo", Name: "Demo",
		Resources: []Resource{
			{Key: "db", Kind: KindDatabase, Engine: "Postgres"},
			{Key: "web", Kind: KindService, Image: "nginx:alpine", Variables: []Variable{{Name: "DATABASE_URL", From: &Reference{Resource: "db", Key: "uri"}}}},
		},
	}
}

func TestValidateRejectsMalformedTemplates(t *testing.T) {
	require.NoError(t, valid().Validate())

	cases := map[string]func(*Template){
		"bad id":                func(t *Template) { t.ID = "Bad ID" },
		"no name":               func(t *Template) { t.Name = " " },
		"no resources":          func(t *Template) { t.Resources = nil },
		"duplicate key":         func(t *Template) { t.Resources[1].Key = "db" },
		"bad key":               func(t *Template) { t.Resources[1].Key = "Web!" },
		"unknown kind":          func(t *Template) { t.Resources[1].Kind = "queue" },
		"database engine":       func(t *Template) { t.Resources[0].Engine = "Oracle" },
		"service with no image": func(t *Template) { t.Resources[1].Image = "" },
		"service with both":     func(t *Template) { t.Resources[1].Repository = "acme/app" },
		"image with spaces":     func(t *Template) { t.Resources[1].Image = "nginx latest" },
		"bad repository":        func(t *Template) { t.Resources[1].Image, t.Resources[1].Repository = "", "not a repo" },
		"function bad runtime": func(t *Template) {
			t.Resources[1] = Resource{Key: "web", Kind: KindFunction, Runtime: "php", Code: "x"}
		},
		"function no code":   func(t *Template) { t.Resources[1] = Resource{Key: "web", Kind: KindFunction, Runtime: "bun"} },
		"database variables": func(t *Template) { t.Resources[0].Variables = []Variable{{Name: "X", Value: "1"}} },
		"bad variable name":  func(t *Template) { t.Resources[1].Variables[0].Name = "1BAD" },
		"duplicate variable": func(t *Template) {
			t.Resources[1].Variables = append(t.Resources[1].Variables, Variable{Name: "DATABASE_URL", Value: "x"})
		},
		"value and from":       func(t *Template) { t.Resources[1].Variables[0].Value = "x" },
		"generate unknown":     func(t *Template) { t.Resources[1].Variables[0] = Variable{Name: "K", Generate: "uuid"} },
		"from missing db":      func(t *Template) { t.Resources[1].Variables[0].From.Resource = "cache" },
		"from non-database":    func(t *Template) { t.Resources[1].Variables[0].From.Resource = "web" },
		"from unknown key":     func(t *Template) { t.Resources[1].Variables[0].From.Key = "token" },
		"port out of range":    func(t *Template) { t.Resources[1].Port = 70000 },
		"too many resources":   func(t *Template) { t.Resources = make([]Resource, MaxResources+1) },
		"oversized value":      func(t *Template) { t.Resources[1].Variables[0] = Variable{Name: "K", Value: strings.Repeat("a", 9000)} },
		"runtime on a service": func(t *Template) { t.Resources[1].Runtime = "bun" },
		"long notes":           func(t *Template) { t.Notes = strings.Repeat("a", 801) },
		"empty size":           func(t *Template) { t.Resources[1].Size = &Size{} },
		"bad cpu":              func(t *Template) { t.Resources[1].Size = &Size{CPU: "lots"} },
		"zero memory":          func(t *Template) { t.Resources[1].Size = &Size{Memory: "0"} },
		"memory off the list":  func(t *Template) { t.Resources[1].Size = &Size{Memory: "6Gi"} },
		"cpu off the list":     func(t *Template) { t.Resources[1].Size = &Size{CPU: "750m"} },
		"same size spelled differently": func(t *Template) {
			t.Resources[1].Size = &Size{Memory: "1024Mi"}
		},
		"memory ceiling below the reserve": func(t *Template) {
			t.Resources[1].Size = &Size{Memory: "2Gi", MemoryMax: "1Gi"}
		},
		"cpu ceiling below the reserve": func(t *Template) {
			t.Resources[1].Size = &Size{CPU: "1", CPUMax: "500m"}
		},
		"ceiling below the default reserve": func(t *Template) { t.Resources[1].Size = &Size{MemoryMax: "64Mi"} },
		"memory ceiling off the list":       func(t *Template) { t.Resources[1].Size = &Size{Memory: "1Gi", MemoryMax: "3Gi"} },
		"cpu ceiling off the list":          func(t *Template) { t.Resources[1].Size = &Size{CPU: "250m", CPUMax: "750m"} },
		"ceiling in the wrong units":        func(t *Template) { t.Resources[1].Size = &Size{Memory: "1Gi", MemoryMax: "2000Mi"} },
		"size on a database":                func(t *Template) { t.Resources[0].Size = &Size{Memory: "1Gi"} },
		"ceiling on a database":             func(t *Template) { t.Resources[0].Size = &Size{MemoryMax: "2Gi"} },
		"files on a database":               func(t *Template) { t.Resources[0].Files = map[string]string{"a.conf": "x"} },
		"files on a function": func(t *Template) {
			t.Resources[1] = Resource{Key: "web", Kind: KindFunction, Runtime: "bun", Code: "x", Files: map[string]string{"a.conf": "x"}}
		},
		"file name with a slash": func(t *Template) { t.Resources[1].Files = map[string]string{"etc/app.conf": "x"} },
		"hidden file name":       func(t *Template) { t.Resources[1].Files = map[string]string{".env": "x"} },
		"file named like a variable": func(t *Template) {
			t.Resources[1].Files = map[string]string{"DATABASE_URL": "x"}
		},
		"oversized file": func(t *Template) {
			t.Resources[1].Files = map[string]string{"big.conf": strings.Repeat("a", MaxFileBytes+1)}
		},
		"too many files": func(t *Template) {
			t.Resources[1].Files = map[string]string{}
			for i := 0; i <= MaxFiles; i++ {
				t.Resources[1].Files[fmt.Sprintf("f%d.conf", i)] = "x"
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			template := valid()
			mutate(&template)
			require.Error(t, template.Validate())
		})
	}
}

func TestAServiceMayHaveSizeAndFiles(t *testing.T) {
	template := valid()
	template.Notes = "Set SITE_URL after you add a domain."
	template.Resources[1].Size = &Size{CPU: "250m", Memory: "512Mi"}
	template.Resources[1].Files = map[string]string{"config.yaml": "a: 1\n"}
	require.NoError(t, template.Validate())

	resources := template.Resources[1].AppResources()
	require.Equal(t, "250m", resources.Requests.Cpu().String())
	require.Equal(t, "512Mi", resources.Limits.Memory().String())
	require.Equal(t, "250m", resources.Limits.Cpu().String(), "requests and limits match, as for a dashboard-created service")
}

func TestAServiceMayReserveLessThanItMayUse(t *testing.T) {
	template := valid()
	template.Resources[1].Size = &Size{CPU: "250m", Memory: "1Gi", CPUMax: "1", MemoryMax: "2Gi"}
	require.NoError(t, template.Validate())

	resources := template.Resources[1].AppResources()
	require.Equal(t, "250m", resources.Requests.Cpu().String())
	require.Equal(t, "1Gi", resources.Requests.Memory().String(), "the reservation is what the capacity check counts")
	require.Equal(t, "1", resources.Limits.Cpu().String())
	require.Equal(t, "2Gi", resources.Limits.Memory().String())
}

// A ceiling can be set for one of the two. What it leaves out stays what it reserves.
func TestACeilingOnOneResourceLeavesTheOtherAtItsReserve(t *testing.T) {
	template := valid()
	template.Resources[1].Size = &Size{CPU: "500m", Memory: "1Gi", MemoryMax: "2Gi"}
	require.NoError(t, template.Validate())

	resources := template.Resources[1].AppResources()
	require.Equal(t, "500m", resources.Limits.Cpu().String(), "no cpu ceiling means cpu is not allowed above its reserve")
	require.Equal(t, "2Gi", resources.Limits.Memory().String())

	template.Resources[1].Size = &Size{MemoryMax: "512Mi"}
	require.NoError(t, template.Validate(), "a ceiling alone sits above the default reserve")
	resources = template.Resources[1].AppResources()
	require.Equal(t, "128Mi", resources.Requests.Memory().String())
	require.Equal(t, "512Mi", resources.Limits.Memory().String())
}

func TestACeilingEqualToTheReserveIsAFixedSize(t *testing.T) {
	template := valid()
	template.Resources[1].Size = &Size{Memory: "1Gi", MemoryMax: "1Gi"}
	require.NoError(t, template.Validate())
	resources := template.Resources[1].AppResources()
	require.Equal(t, resources.Requests, resources.Limits)
}

func TestASizeWithOnlyMemoryKeepsTheDefaultCPU(t *testing.T) {
	resource := Resource{Size: &Size{Memory: "2Gi"}}
	resources := resource.AppResources()
	require.Equal(t, "2Gi", resources.Requests.Memory().String())
	defaults := platform.DefaultAppResources().Requests
	require.Equal(t, defaults.Cpu().String(), resources.Requests.Cpu().String())
	require.Equal(t, platform.DefaultAppResources(), Resource{}.AppResources(), "no size means the default")
}

// The size picker in the dashboard can only show and change the sizes on its lists, so a template may only
// name those, and a service made from one is exactly what the picker would make.
func TestATemplateSizeIsExactlyWhatTheSizePickerWouldMake(t *testing.T) {
	for _, cpu := range platform.CPUSizes() {
		for _, memory := range platform.MemorySizes() {
			size := &Size{CPU: cpu.Value, Memory: memory.Value}
			template := valid()
			template.Resources[1].Size = size
			require.NoError(t, template.Validate(), "%s / %s", cpu.Value, memory.Value)

			picked, err := platform.ResourcesFromSize(cpu.Value, memory.Value)
			require.NoError(t, err)
			require.Equal(t, picked, template.Resources[1].AppResources())
		}
	}
}

// The same holds for the ceiling: any pair of sizes with the ceiling at or above the reserve is one the picker can
// make, and any with the ceiling below it is refused.
func TestACeilingIsExactlyWhatTheSizePickerWouldMake(t *testing.T) {
	memories := platform.MemorySizes()
	for i, reserve := range memories {
		for j, ceiling := range memories {
			template := valid()
			template.Resources[1].Size = &Size{CPU: "250m", Memory: reserve.Value, CPUMax: "1", MemoryMax: ceiling.Value}
			if j < i {
				err := template.Validate()
				require.Error(t, err, "%s up to %s", reserve.Value, ceiling.Value)
				require.Contains(t, err.Error(), "cannot be below")
				continue
			}
			require.NoError(t, template.Validate(), "%s up to %s", reserve.Value, ceiling.Value)
			picked, err := platform.ResourcesFromSizes("250m", reserve.Value, "1", ceiling.Value)
			require.NoError(t, err)
			require.Equal(t, picked, template.Resources[1].AppResources())
		}
	}
}

func TestASizeOffTheListSaysWhichSizesAreOffered(t *testing.T) {
	template := valid()
	template.Resources[1].Size = &Size{Memory: "6Gi"}
	err := template.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "100m, 250m, 500m, 1, 2")
	require.Contains(t, err.Error(), "128Mi, 256Mi, 512Mi, 1Gi, 2Gi, 4Gi")

	template.Resources[1].Size = &Size{Memory: "1Gi", MemoryMax: "3Gi"}
	err = template.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "128Mi, 256Mi, 512Mi, 1Gi, 2Gi, 4Gi", "a ceiling is held to the same list")
}

func TestACeilingBelowTheReserveSaysSo(t *testing.T) {
	template := valid()
	template.Resources[1].Size = &Size{Memory: "2Gi", MemoryMax: "1Gi"}
	err := template.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot be below the memory it reserves")
}

func TestGenerateSecretIsRandomHex(t *testing.T) {
	a, err := GenerateSecret()
	require.NoError(t, err)
	b, err := GenerateSecret()
	require.NoError(t, err)
	require.Len(t, a, 32)
	require.NotEqual(t, a, b)
	require.Regexp(t, "^[0-9a-f]+$", a)
}

func TestSummaryCountsResourceKinds(t *testing.T) {
	require.Equal(t, "1 service, 1 database", valid().Summary())
	infisical, _ := Find("infisical")
	require.Equal(t, "1 service, 2 databases", infisical.Summary())
}

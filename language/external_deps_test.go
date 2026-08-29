package language

import (
	"os"
	"path/filepath"
	"testing"
)

// The umbrella (compile-time) and Maven (POM) halves of external gencode
// deps must move together: an umbrella addition without a Maven mirror
// recreates the consumer NoClassDefFoundError the pairing exists to prevent
// (see ExternalProtoDeps.JavaMavenDeps).
func TestDetectExternalProtoImportsPairsUmbrellaAndMavenDeps(t *testing.T) {
	cases := []struct {
		name          string
		imports       string
		wantJava      []string
		wantMavenDeps []string
	}{
		{
			name: "none",
		},
		{
			name:     "googleapis",
			imports:  `import "google/api/field_behavior.proto";`,
			wantJava: []string{"@googleapis//google/api:api_java_proto"},
			wantMavenDeps: []string{
				"com.google.api.grpc:proto-google-common-protos:$${GOOGLE_COMMON_PROTOS_VERSION:-2.75.0}",
			},
		},
		{
			name:     "longrunning",
			imports:  `import "google/longrunning/operations.proto";`,
			wantJava: []string{"@googleapis//google/longrunning:longrunning_java_proto"},
			wantMavenDeps: []string{
				"com.google.api.grpc:proto-google-common-protos:$${GOOGLE_COMMON_PROTOS_VERSION:-2.75.0}",
			},
		},
		{
			name:     "protovalidate",
			imports:  `import "buf/validate/validate.proto";`,
			wantJava: []string{"//:protovalidate_java_proto"},
			wantMavenDeps: []string{
				"build.buf:protovalidate:$${PROTOVALIDATE_MAVEN_VERSION:-1.2.2}",
			},
		},
		{
			name: "all",
			imports: `import "google/api/field_behavior.proto";
import "google/longrunning/operations.proto";
import "buf/validate/validate.proto";`,
			wantJava: []string{
				"@googleapis//google/api:api_java_proto",
				"@googleapis//google/longrunning:longrunning_java_proto",
				"//:protovalidate_java_proto",
			},
			wantMavenDeps: []string{
				"com.google.api.grpc:proto-google-common-protos:$${GOOGLE_COMMON_PROTOS_VERSION:-2.75.0}",
				"build.buf:protovalidate:$${PROTOVALIDATE_MAVEN_VERSION:-1.2.2}",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			proto := "syntax = \"proto3\";\npackage x;\n" + tc.imports + "\nmessage X { string id = 1; }\n"
			if err := os.WriteFile(filepath.Join(dir, "x.proto"), []byte(proto), 0644); err != nil {
				t.Fatal(err)
			}

			got := detectExternalProtoImports(dir)
			assertStringSlice(t, "Java", got.Java, tc.wantJava)
			assertStringSlice(t, "JavaMavenDeps", got.JavaMavenDeps, tc.wantMavenDeps)

			// The invariant this test exists for: any umbrella entry implies
			// a non-empty Maven mirror set.
			if len(got.Java) > 0 && len(got.JavaMavenDeps) == 0 {
				t.Errorf("umbrella deps %v have no Maven POM mirror — consumer class-load breakage", got.Java)
			}
		})
	}
}

func assertStringSlice(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s[%d] = %q, want %q", label, i, got[i], want[i])
		}
	}
}

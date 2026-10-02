package swagger

import (
	"encoding/json"
	"testing"
)

func TestRoomRoleAllowsNull(t *testing.T) {
	var document struct {
		Definitions map[string]struct {
			Properties map[string]struct {
				Nullable bool `json:"x-nullable"`
			} `json:"properties"`
		} `json:"definitions"`
	}
	if err := json.Unmarshal([]byte(SwaggerInfo.ReadDoc()), &document); err != nil {
		t.Fatalf("decode Swagger: %v", err)
	}
	if !document.Definitions["internal_chat.Room"].Properties["role"].Nullable {
		t.Fatal("public nonmember room responses require a nullable role schema")
	}
}

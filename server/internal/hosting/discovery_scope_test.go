package hosting

import (
	"encoding/json"
	"testing"
)

// A customer's PHP page counts the pools of their own websites only.
func TestForScopeCountsOnlyTheOwnersPools(t *testing.T) {
	inv := `{"websites":{"websites":[
		{"domain":"a.test","php":{"version":"8.4","pool":"pa"}},
		{"domain":"b.test","php":{"version":"8.4","pool":"pb"}}]},
	 "php":{"versions":[{"version":"8.3","pool_count":1},{"version":"8.4","pool_count":2}],
	        "pools":[{"name":"pa","version":"8.4"},{"name":"pb","version":"8.4"},{"name":"pc","version":"8.3"}]}}`
	out := ForScope(json.RawMessage(inv), false, []string{"a.test"})
	var got struct {
		PHP struct {
			Versions []struct {
				Version   string `json:"version"`
				PoolCount int    `json:"pool_count"`
			} `json:"versions"`
			Pools []struct {
				Name string `json:"name"`
			} `json:"pools"`
		} `json:"php"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.PHP.Pools) != 1 || got.PHP.Pools[0].Name != "pa" {
		t.Errorf("pools = %+v", got.PHP.Pools)
	}
	counts := map[string]int{}
	for _, v := range got.PHP.Versions {
		counts[v.Version] = v.PoolCount
	}
	if counts["8.4"] != 1 || counts["8.3"] != 0 {
		t.Errorf("pool counts = %v, want 8.4:1 8.3:0", counts)
	}
	// Administrators see the server's numbers.
	all := ForScope(json.RawMessage(inv), true, nil)
	if string(all) != inv {
		t.Error("the administrator's inventory was changed")
	}
}

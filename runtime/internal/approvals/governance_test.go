package approvals

import "testing"

// 治理判据 1/13：决策的回执与 timline 行必须带**出处**与**依据**。
// 没有这两样，"这条经验凭什么"就答不出来了。
func TestDecisionCarriesBasisAndDerivedFrom(t *testing.T) {
	p := Pending{ID: "p-1", Title: "t", Who: "alice", To: "manager", Body: "b"}
	d := Decision{ProposalID: "p-1", By: "manager", Why: "同意", Via: "board"}

	rec := d.AuditRecord(p)
	if rec.Basis != "proposal:p-1" {
		t.Fatalf("审计记录该带 basis=proposal:p-1，拿到 %q", rec.Basis)
	}
	if len(rec.DerivedFrom) != 1 || rec.DerivedFrom[0] != "proposal:p-1" {
		t.Fatalf("审计记录该带 derived_from=[proposal:p-1]，拿到 %v", rec.DerivedFrom)
	}
	if rec.Role == "" {
		t.Fatal("审计记录必须有 role（判据 4）")
	}
	// 回执记在「该谁批」的岗位上：这是按角色归因，不是按个人。
	if rec.Role != "manager" {
		t.Fatalf("role 该是 manager，拿到 %q", rec.Role)
	}
}

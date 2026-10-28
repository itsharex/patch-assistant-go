package tree

import (
	"testing"
	"time"
)

// TestMappedNodeModifyTimeFollowsBoundNode 回归测试（对应需求 F 的后续反馈
// "映射节点没有时间"）：
//
// Java 的 MappedNode **覆写**了 getModifyTime()，直接返回被绑定节点的时间
// （见 MappedNode.java）。Go 没有多态，必须在基类 ModifyTime() 里按 KindMapped
// 转发 —— 否则"补丁里有、应用里没有"的文件（映射节点）自己那三个时间字段
// 都是零值，界面"修改时间"那一列就是空的。
func TestMappedNodeModifyTimeFollowsBoundNode(t *testing.T) {
	t0 := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	tm := time.Date(2026, 9, 3, 11, 1, 52, 0, time.UTC)
	src := NewCompressFile("Foo.class", "Foo.class", 100, 0, t0, tm, t0, nil, true)

	mp := NewMappedNode(src)
	if mp.Kind != KindMapped {
		t.Fatalf("应为 KindMapped，实际 %v", mp.Kind)
	}
	if mp.ModifyTime().IsZero() {
		t.Fatalf("映射节点的 ModifyTime 不应为零值（应跟随被绑定节点）")
	}
	if !mp.ModifyTime().Equal(tm) {
		t.Fatalf("映射节点的 ModifyTime 应等于被绑定节点的时间：期望 %v 实际 %v", tm, mp.ModifyTime())
	}

	// 绑定关系改变后应动态跟随（Java 是覆写方法，天然动态）。
	tm2 := time.Date(2027, 2, 2, 8, 30, 0, 0, time.UTC)
	src2 := NewCompressFile("Foo.class", "Foo.class", 100, 0, t0, tm2, t0, nil, true)
	mp.SetMappedNode(src2)
	if !mp.ModifyTime().Equal(tm2) {
		t.Fatalf("重新绑定后 ModifyTime 应跟随新节点：期望 %v 实际 %v", tm2, mp.ModifyTime())
	}
}

// TestMappedNodeModifyTimeWithoutBoundNode 被绑定节点为空时不能 panic，
// 退回自身字段（零值）。
func TestMappedNodeModifyTimeWithoutBoundNode(t *testing.T) {
	mp := &Node{Kind: KindMapped}
	if !mp.ModifyTime().IsZero() {
		t.Fatalf("无绑定节点时应返回零值，实际 %v", mp.ModifyTime())
	}
}

// TestNonMappedModifyTimeUnaffected 非映射节点的 ModifyTime 行为不变。
func TestNonMappedModifyTimeUnaffected(t *testing.T) {
	tm := time.Date(2021, 6, 6, 6, 6, 6, 0, time.UTC)
	n := NewCompressFile("a.txt", "a.txt", 1, 0, tm, tm, tm, nil, false)
	if !n.ModifyTime().Equal(tm) {
		t.Fatalf("普通节点的 ModifyTime 应返回自身字段：期望 %v 实际 %v", tm, n.ModifyTime())
	}
}

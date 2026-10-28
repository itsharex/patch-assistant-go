package util

import (
	"fmt"
	"testing"
)

// 这一组守的是 maxMyersD 那道闸的**口径**。
//
// 曾经的写法是 `if n+m > maxMyersD { return nil }` —— 量的是两份文本的总行数，
// 而 n+m 只是编辑距离的上界。后果：两份 1000 行的文件哪怕只差一行，
// 也会被判成"整段被替换"，界面上整个文件都被标成差异。
// 用户实测踩到过（一个上千行的反编译类，两版只差几行，整篇全被染成"改"）。

func mkLines(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("line %d", i)
	}
	return out
}

// 大文件只差一行 —— 必须精确到那一行，不能退化成"整篇替换"。
func TestDeltasLargeFileWithOneChange(t *testing.T) {
	const n = 1200
	a := mkLines(n)
	b := mkLines(n)
	b[600] = "line 600 CHANGED"

	got := Deltas(a, b)
	if len(got) != 1 {
		t.Fatalf("期望 1 个差异块，实际 %d 个（退回整段替换了？）", len(got))
	}
	d := got[0]
	if d.OrigStart != 600 || d.RevStart != 600 {
		t.Errorf("起点不对：OrigStart=%d RevStart=%d，期望都是 600", d.OrigStart, d.RevStart)
	}
	if len(d.OrigLines) != 1 || len(d.RevLines) != 1 {
		t.Fatalf("差异块大小不对：左 %d 行、右 %d 行，期望各 1 行",
			len(d.OrigLines), len(d.RevLines))
	}
	if d.OrigLines[0] != "line 600" || d.RevLines[0] != "line 600 CHANGED" {
		t.Errorf("差异内容不对：%q -> %q", d.OrigLines[0], d.RevLines[0])
	}
}

// 改动落在**第一行**时没有公共前缀可剥，闸门只能靠真实步数兜住 ——
// 这一条专门盯 d 的口径：退回 `n+m > maxMyersD` 就会红。
func TestDeltasLargeFileChangedAtVeryStart(t *testing.T) {
	const n = 1200
	a := mkLines(n)
	b := mkLines(n)
	a[0] = "line 0 CHANGED"

	got := Deltas(a, b)
	if len(got) != 1 {
		t.Fatalf("期望 1 个差异块，实际 %d 个（n+m=2400 撞了闸？）", len(got))
	}
	if got[0].OrigStart != 0 || len(got[0].OrigLines) != 1 {
		t.Errorf("差异块不对：OrigStart=%d，行数=%d，期望 0 / 1",
			got[0].OrigStart, len(got[0].OrigLines))
	}
	if got[0].RevStart != 0 || len(got[0].RevLines) != 1 {
		t.Errorf("右栏差异块不对：RevStart=%d，行数=%d，期望 0 / 1",
			got[0].RevStart, len(got[0].RevLines))
	}
}

// 后缀**不能**剥 —— 这一点跟前缀不一样，别"顺手对称地"加上去。
//
// 重复行会让最小编辑脚本不唯一，java-diff-utils 选哪一份取决于搜索起点。
// 剥掉公共后缀会改变选路：实测把后缀也剥掉后，132 条跨语言语料里红 22 条。
// 下面这条用例就是其中之一（尾部有重复行）。
func TestDeltasDoesNotTrimCommonSuffix(t *testing.T) {
	// 两份文本按 SplitDiffLines 切出来分别是
	// ["", "}", "a", "}"] 与 ["", "x", "}"]
	left := "\n}\na\n}"
	right := "\nx\n}"

	// 与 java-diff-utils 一致：留 "" → 插 "x" → 留 "}" → 删 "a" → 删 "}"
	const want = "--- L.txt\n+++ R.txt\n@@ -0,0 +0,0 @@\n \n@@ -2,0 +2,1 @@\n+x\n }\n@@ -3,2 +4,0 @@\n-a\n-}"
	if got := Diff2Str(left, right, "L.txt", "R.txt"); got != want {
		t.Errorf("编辑脚本选路变了\n期望 = %q\n实际 = %q", want, got)
	}
}

// 差异块的下标必须是**原文里的绝对行号** —— 剥公共前缀时很容易漏掉偏移。
func TestDeltasIndicesAreAbsoluteAfterPrefixTrim(t *testing.T) {
	a := []string{"a", "b", "c", "d", "e"}
	b := []string{"a", "b", "X", "d", "e"}

	got := Deltas(a, b)
	if len(got) != 1 {
		t.Fatalf("期望 1 个差异块，实际 %d 个", len(got))
	}
	if got[0].OrigStart != 2 || got[0].RevStart != 2 {
		t.Errorf("起点没有算上前缀偏移：OrigStart=%d RevStart=%d，期望都是 2",
			got[0].OrigStart, got[0].RevStart)
	}
}

// 差异只在开头 / 只在结尾 —— 前缀、后缀两条裁剪路径都要覆盖。
func TestDeltasPrefixOnlyAndSuffixOnly(t *testing.T) {
	base := []string{"a", "b", "c", "d", "e"}

	head := []string{"HEAD", "b", "c", "d", "e"}
	if d := Deltas(base, head); len(d) != 1 || d[0].OrigStart != 0 || len(d[0].OrigLines) != 1 {
		t.Errorf("开头差异：%+v", d)
	}

	tail := []string{"a", "b", "c", "d", "TAIL"}
	if d := Deltas(base, tail); len(d) != 1 || d[0].OrigStart != 4 || len(d[0].OrigLines) != 1 {
		t.Errorf("结尾差异：%+v", d)
	}
}

// 两份完全一样 —— 不该产生任何差异块（旧实现撞闸后会返回"整篇替换"）。
func TestDeltasIdenticalLargeInputsProduceNoDelta(t *testing.T) {
	a := mkLines(1500)
	b := mkLines(1500)
	if d := Deltas(a, b); len(d) != 0 {
		t.Errorf("相同的文本不该有差异块，实际 %d 个（第一个：%+v）", len(d), d[0])
	}
}

// 两份完全一样的小输入也不该有差异块。
func TestDeltasIdenticalSmallInputsProduceNoDelta(t *testing.T) {
	a := []string{"a", "b", "c"}
	if d := Deltas(a, append([]string(nil), a...)); len(d) != 0 {
		t.Errorf("相同的文本不该有差异块，实际 %d 个", len(d))
	}
}

// 差异又多又散时**仍然**要退回"整段替换" —— 这道闸不能因为上面几条就拆掉，
// 否则差异稍大的文件会把内存吃光（trace 每步一个快照）。
func TestDeltasTotallyDifferentInputsStillBail(t *testing.T) {
	a := mkLines(1500)
	b := make([]string, 1500)
	for i := range b {
		b[i] = fmt.Sprintf("other %d", i)
	}
	got := Deltas(a, b)
	if len(got) != 1 {
		t.Fatalf("期望退回单个整段替换块，实际 %d 个", len(got))
	}
	if len(got[0].OrigLines) != len(a) || len(got[0].RevLines) != len(b) {
		t.Errorf("整段替换块没盖住全文：左 %d 行、右 %d 行",
			len(got[0].OrigLines), len(got[0].RevLines))
	}
}

// 空输入的历史口径不能变：Deltas(nil, nil) 仍返回一个"两边都空"的块
// （DiffLines 依赖它走 `@@ -1,0 +1,0 @@` 那条分支，别顺手改成 nil）。
func TestDeltasEmptyInputsKeepLegacyShape(t *testing.T) {
	got := Deltas(nil, nil)
	if len(got) != 1 {
		t.Fatalf("期望 1 个空差异块，实际 %d 个", len(got))
	}
	if len(got[0].OrigLines) != 0 || len(got[0].RevLines) != 0 {
		t.Errorf("空差异块不该带行：左 %d、右 %d", len(got[0].OrigLines), len(got[0].RevLines))
	}
}

// 纯增 / 纯删（一边为空）时不能把公共前后缀算进去。
func TestDeltasPureInsertAndDelete(t *testing.T) {
	a := []string{"a", "b", "c"}
	ins := []string{"a", "NEW", "b", "c"}
	d := Deltas(a, ins)
	if len(d) != 1 {
		t.Fatalf("纯插入期望 1 个差异块，实际 %d 个", len(d))
	}
	if len(d[0].OrigLines) != 0 || len(d[0].RevLines) != 1 {
		t.Errorf("纯插入应只有右侧 1 行：左 %d、右 %d", len(d[0].OrigLines), len(d[0].RevLines))
	}
	if d[0].RevStart != 1 {
		t.Errorf("插入位置不对：RevStart=%d，期望 1", d[0].RevStart)
	}

	del := []string{"a", "c"}
	d2 := Deltas(a, del)
	if len(d2) != 1 || len(d2[0].OrigLines) != 1 || len(d2[0].RevLines) != 0 {
		t.Errorf("纯删除应只有左侧 1 行：%+v", d2)
	}
	if d2[0].OrigStart != 1 {
		t.Errorf("删除位置不对：OrigStart=%d，期望 1", d2[0].OrigStart)
	}
}

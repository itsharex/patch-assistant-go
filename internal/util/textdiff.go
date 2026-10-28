package util

import "strings"

// 文本比较，移植自 Java cn.cpoet.patch.assistant.util.TextDiffUtil。
//
// 输出格式**不是**标准的 unified diff，而是 java-diff-utils 那个
// `UnifiedDiffUtils.generateOriginalAndDiff` 的产物，有三点必须照搬：
//
//  1. 头两行固定是 `--- <原名>` / `+++ <新名>`。原作者在 TextDiffUtil.diff 里
//     手动补过一次 `diffs.set(1, "+++ " + revisedName)`，说明那个库原本在
//     第 1 行放的是裸名字 —— 照最终结果对齐即可。
//  2. **首个差异之前的不变行**会单独起一个 `@@ -0,0 +0,0 @@` 的块（那个
//     全 0 的区间号是个占位符，不是真实行号）。两份文本完全相同时，
//     整个原文都落在这个块里。
//  3. 两个差异之间的不变行**全部**跟在**前一个**差异块后面，没有"只留
//     3 行上下文"的说法；尾部的不变行同理，跟在最后一个差异块后面。
//     这一点是实测出来的（见 _extract/tools/DiffProbe2.java 的探针输出），
//     与教科书上的 unified diff 分组规则不同，别按直觉改。
//
// 差异本身用 Myers 算法求最小编辑脚本，与 java-diff-utils 同源。
// 本来担心"有大量重复行时最小脚本不唯一、选取结果可能不同"，但实测下来
// 132 条语料（含 120 条从极小字母表生成的、必然大量重复行的随机用例）
// 与原版**逐字节一致**，见 textdiff_golden_test.go 的跨语言基准。
// 说明 Myers 前向搜索 + trace 回溯的选路顺序与 java-diff-utils 一致。

// DiffLines 比较两段文本，返回差异行列表。
// 对应 Java TextDiffUtil.diff。
func DiffLines(original, revised, originalName, revisedName string) []string {
	left := SplitDiffLines(original)
	right := SplitDiffLines(revised)
	deltas := Deltas(left, right)

	out := []string{"--- " + originalName, "+++ " + revisedName}
	if len(deltas) == 0 {
		// 没有任何差异：整个原文算"首个差异之前的不变行"
		out = append(out, "@@ -0,0 +0,0 @@")
		for _, l := range left {
			out = append(out, " "+l)
		}
		return out
	}

	for i, d := range deltas {
		if i == 0 {
			if pre := left[:d.OrigStart]; len(pre) > 0 {
				out = append(out, "@@ -0,0 +0,0 @@")
				for _, l := range pre {
					out = append(out, " "+l)
				}
			}
		}
		out = append(out, "@@ -"+itoa(d.OrigStart+1)+","+itoa(len(d.OrigLines))+
			" +"+itoa(d.RevStart+1)+","+itoa(len(d.RevLines))+" @@")
		for _, l := range d.OrigLines {
			out = append(out, "-"+l)
		}
		for _, l := range d.RevLines {
			out = append(out, "+"+l)
		}
		// 这个差异之后、下一个差异之前的不变行，全部挂在当前块后面
		next := len(left)
		if i+1 < len(deltas) {
			next = deltas[i+1].OrigStart
		}
		for _, l := range left[d.OrigStart+len(d.OrigLines) : next] {
			out = append(out, " "+l)
		}
	}
	return out
}

// Diff2Str 比较并拼接成字符串。对应 Java TextDiffUtil.diff2Str。
func Diff2Str(original, revised, originalName, revisedName string) string {
	return DiffJoin(DiffLines(original, revised, originalName, revisedName))
}

// DiffJoin 用换行拼接。对应 Java TextDiffUtil.diffJoin。
func DiffJoin(lines []string) string { return strings.Join(lines, "\n") }

// SplitDiffLines 按 Java `original.split("\n|(\r\n)")` 的口径切行。
//
// 三个容易写错的地方：
//   - `\r\n` 会被整体吃掉（正则在那个位置先试 `\n` 不中、再试 `\r\n` 命中），
//     所以切出来的行**不带**尾部的 `\r`；
//   - 空串切出来是**一个**空行（Java 对空输入返回长度为 1 的数组）；
//   - 尾部的空行会被去掉（Java split 的 limit=0 行为），
//     `"a\nb\n"` 切出来是 `["a","b"]` 而不是 `["a","b",""]`。
func SplitDiffLines(s string) []string {
	if s == "" {
		return []string{""}
	}
	out := make([]string, 0, strings.Count(s, "\n")+1)
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
			continue
		}
		if s[i] == '\r' && i+1 < len(s) && s[i+1] == '\n' {
			out = append(out, s[start:i])
			i++
			start = i + 1
		}
	}
	out = append(out, s[start:])
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// Delta 一段行级差异：原文的 [OrigStart, OrigStart+len(OrigLines)) 被替换成
// 新文的 [RevStart, RevStart+len(RevLines))。两边都非空就是"修改"，
// 一边为空就是纯增/纯删。
//
// 导出是给界面用的：双栏预览要给差异行着色。原版双栏**不做任何标记**，
// 这是刻意的增强，见 MIGRATION.md。
type Delta struct {
	OrigStart int
	OrigLines []string
	RevStart  int
	RevLines  []string
}

// maxMyersD 是 Myers 前向搜索的**步数**上限（d = 当前已走到的编辑距离）。
//
// 这里量的是真实步数，**不是 n+m**。n+m 只是编辑距离的上界，拿它当闸门会把
// "大文件只差几行"也一并拦掉 —— 那时 myersOps 返回 nil，Deltas 退化成
// "整篇被替换"，界面上表现为**整个文件都被标成差异**。用户实测踩到过：
// 一个上千行的反编译类、两版只差几行，整篇却全被染成"改"。
//
// 真正要防的是 d 很大时 trace 的内存（每步一个快照，每个长 2*maxD+2）。
// 上游没有这道闸（它靠 O(ND) 的时间硬扛），属刻意的健壮性偏差，
// 已登记在 MIGRATION.md。
const maxMyersD = 2000

// maxMyersCells 是 trace 的存储上限，单位是 int 个数（约 32 MB）。
//
// 大文件即使步数没超，**每个快照本身**也很长（2*(n+m)+2），光靠步数上限
// 框不住内存 —— 两个上限一起用才真正把存储钉死。撞到这个上限说明差异
// 又多又散，逐块对比对用户也没什么参考价值，直接退回"整段替换"。
const maxMyersCells = 4 << 20

// Deltas 对两份**已经切好的行**求差异块。
//
// 入参是切好的行而不是原始文本，因为调用方的切行口径不止一种：界面按
// JavaFX CodeArea 的段落口径（content.Paragraphs），差异模式按
// SplitDiffLines。标记要落在哪套行号上就传哪套切好的行 —— 这样高亮与
// 渲染出来的行号才能一一对应。
func Deltas(a, b []string) []Delta {
	// 只剥**公共前缀**，不剥公共后缀。
	//
	// 为什么剥前缀是安全的：d=0 时那条"蛇"本来就沿着对角线一路吃掉公共前缀，
	// 之后搜索从 (p,p) 出发。剥掉前缀再搜，只是把整张坐标表平移了 p ——
	// 每一步的比较、回溯的选路都逐位对应，结果完全一致。
	//
	// 为什么**不能**剥后缀：重复行会让最小编辑脚本不唯一，
	// java-diff-utils 选哪一份取决于搜索起点。实测把后缀也剥掉后，
	// 132 条跨语言语料里红 22 条（`a=["", "}", "a", "}"]` vs
	// `b=["", "x", "}"]` 这种"尾部有重复行"的用例选路会变）。
	// 见 TestDeltasDoesNotTrimCommonSuffix。
	//
	// 剥前缀的收益是实打实的：用户踩到的场景是"上千行、只差几行"，
	// 剥掉前缀后中间段短得多，Myers 的宽度（2*(n+m)+2）和步数都跟着降。
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	midA, midB := a[p:], b[p:]

	ops := myersOps(midA, midB)
	if ops == nil {
		// 超出上限：整段替换（前缀是公共的，不进差异块）
		if p == 0 {
			return []Delta{{OrigStart: 0, OrigLines: a, RevStart: 0, RevLines: b}}
		}
		if len(midA) == 0 && len(midB) == 0 {
			return nil // 两份文本完全一样
		}
		return []Delta{{OrigStart: p, OrigLines: midA, RevStart: p, RevLines: midB}}
	}

	var deltas []Delta
	i, j := p, p // 原文/新文的下标（跳过公共前缀）
	k := 0       // ops 下标
	for k < len(ops) {
		switch ops[k] {
		case opEqual:
			i++
			j++
			k++
		case opDelete, opInsert:
			// 把连续的删除和插入收成一个块：先攒删除、再攒插入。
			// 顺序固定成"先 - 后 +"，与 java-diff-utils 的输出一致。
			startI, startJ := i, j
			var del, ins []string
			for k < len(ops) && ops[k] == opDelete {
				del = append(del, a[i])
				i++
				k++
			}
			for k < len(ops) && ops[k] == opInsert {
				ins = append(ins, b[j])
				j++
				k++
			}
			// 有可能先遇到插入再遇到删除（脚本顺序不同），再兜一次
			for k < len(ops) && ops[k] == opDelete {
				del = append(del, a[i])
				i++
				k++
			}
			deltas = append(deltas, Delta{
				OrigStart: startI, OrigLines: del,
				RevStart: startJ, RevLines: ins,
			})
		}
	}
	return deltas
}

type opKind int

const (
	opEqual opKind = iota
	opDelete
	opInsert
)

// myersOps 用 Myers 算法求最小编辑脚本，返回按顺序排列的操作序列。
// 超出 maxMyersD 时返回 nil。
//
// 算法出自 Myers 1986 那篇论文的贪心版本；java-diff-utils 的 MyersDiff
// 是同一套，所以"最小脚本不唯一"时选取的结果大概率也一致。
func myersOps(a, b []string) []opKind {
	n, m := len(a), len(b)
	if n == 0 && m == 0 {
		return nil
	}
	maxD := n + m
	offset := maxD
	width := 2*maxD + 2
	v := make([]int, width)
	trace := make([][]int, 0, maxMyersD+1)

	for d := 0; d <= maxD; d++ {
		// 两个上限一起框内存：步数（d）与快照总长（d*width）。
		// 撞上任意一个都说明差异太多，交给调用方退回"整段替换"。
		if d > maxMyersD || d*width > maxMyersCells {
			return nil
		}
		snap := make([]int, width)
		copy(snap, v)
		trace = append(trace, snap)

		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
				x = v[offset+k+1]
			} else {
				x = v[offset+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[offset+k] = x
			if x >= n && y >= m {
				return backtrack(trace, a, b, offset)
			}
		}
	}
	return nil
}

// backtrack 沿着 trace 反推编辑脚本。
func backtrack(trace [][]int, a, b []string, offset int) []opKind {
	var rev []opKind
	x, y := len(a), len(b)

	for d := len(trace) - 1; d >= 0; d-- {
		v := trace[d]
		k := x - y
		var prevK int
		if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := v[offset+prevK]
		prevY := prevX - prevK

		// 先退回对角线（相等的行）
		for x > prevX && y > prevY {
			rev = append(rev, opEqual)
			x--
			y--
		}
		if d == 0 {
			break
		}
		if x == prevX {
			rev = append(rev, opInsert) // 从新文里插入了一行
		} else {
			rev = append(rev, opDelete) // 从原文里删掉了一行
		}
		x, y = prevX, prevY
	}

	// 反转
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

// itoa 是不引 strconv 的小整数转字符串。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

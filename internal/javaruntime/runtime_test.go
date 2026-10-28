//go:build windows

package javaruntime

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDirExtractsRuntime 钉住"解压出来的运行时里该有的东西都在"。
//
// 这是本包唯一真正重要的行为：少一个 `bin/javac.exe`，「编译为 .class」
// 就会在真机上失败，而那时错误信息只会说"找不到 javac"，指不到这里。
func TestDirExtractsRuntime(t *testing.T) {
	d, err := Dir()
	if err != nil {
		t.Fatalf("Dir() 失败：%v", err)
	}
	for _, rel := range []string{"bin/java.exe", "bin/javac.exe", "lib/modules", readyMark} {
		p := filepath.Join(d, rel)
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() {
			t.Errorf("运行时缺少 %s（%s）", rel, p)
		}
	}
}

// TestDirIsStable 钉住"重复调用不重复解压、路径稳定"。
func TestDirIsStable(t *testing.T) {
	a, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	b, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("两次 Dir() 给了不同路径：%q vs %q", a, b)
	}
}

// TestJavaAndJavacPaths 钉住两个可执行文件的落点。
func TestJavaAndJavacPaths(t *testing.T) {
	j, err := Java()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(j) != "java.exe" {
		t.Errorf("java 路径不对：%s", j)
	}
	jc, err := Javac()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(jc) != "javac.exe" {
		t.Errorf("javac 路径不对：%s", jc)
	}
}

// TestRuntimeDirHasVersion 钉住"解压目录名带版本号"。
//
// 不带版本号的话，换了运行时（升级 JDK）会复用旧目录，标记又刚好对不上，
// 于是每次启动都重解压 —— 或者更糟，用着新旧混合的目录。
func TestRuntimeDirHasVersion(t *testing.T) {
	d, err := runtimeDir()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(d) != "runtime-"+runtimeVersion {
		t.Errorf("目录名应带版本号，实际 %s", filepath.Base(d))
	}
}

// TestSafeJoinRejectsEscape 钉住 zip slip 防护。
func TestSafeJoinRejectsEscape(t *testing.T) {
	if _, err := safeJoin(`C:\x`, `../evil.exe`); err == nil {
		t.Error("应当拒绝 ../ 逃逸")
	}
	if _, err := safeJoin(`C:\x`, `bin/java.exe`); err != nil {
		t.Errorf("正常条目不该被拒：%v", err)
	}
}

// TestDisabledSkipsBundled 钉住逃生口：设了环境变量就不走内置运行时。
//
// 调用方（decompile.FindJava / javac.FindJavac）靠 Dir() 返回错误来退回
// 本机 Java，所以这里失败是**预期行为**，不是缺陷。
func TestDisabledSkipsBundled(t *testing.T) {
	if Disabled() {
		t.Fatal("没设环境变量时 Disabled() 应当为 false")
	}
	t.Setenv(envNoBundled, "1")
	if !Disabled() {
		t.Fatal("设了环境变量后 Disabled() 应当为 true")
	}
	if _, err := Dir(); err == nil {
		t.Error("Disabled 时 Dir() 应当失败，好让调用方退回本机 Java")
	}
	if _, err := Java(); err == nil {
		t.Error("Disabled 时 Java() 应当失败")
	}
}

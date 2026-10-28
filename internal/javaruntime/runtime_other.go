//go:build !windows

// Package javaruntime 定位内置 JDK 运行时。
//
// 非 Windows 平台上没有内置运行时 —— 内嵌的是 Windows 的 jlink 产物
// （`bin/java.exe` 那一套），所以这里只提供"不可用"的答案，调用方据此
// 退回"找本机 Java"。本项目实际只发布 Windows exe，这条分支的存在是为了
// 让代码在其它平台上仍能编译、跑单测。
package javaruntime

import "errors"

// ErrUnsupported 表示当前平台没有内置运行时。
var ErrUnsupported = errors.New("内置运行时仅支持 Windows")

// Dir 在非 Windows 平台上总是失败。
func Dir() (string, error) { return "", ErrUnsupported }

// Java 在非 Windows 平台上总是失败。
func Java() (string, error) { return "", ErrUnsupported }

// Javac 在非 Windows 平台上总是失败。
func Javac() (string, error) { return "", ErrUnsupported }

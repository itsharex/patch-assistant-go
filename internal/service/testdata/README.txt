PatchAssistant2J 补丁说明示例

== 形式 A：前缀与路径之间有空格（文档直觉写法）==
+ app/BOOT-INF/classes/com/demo/NewService.class
! app/BOOT-INF/classes/com/demo/OldService.class
- app/BOOT-INF/classes/com/demo/Removed.class
? app/BOOT-INF/classes/com/demo/Ignore.class

== 形式 B：前缀紧贴路径（无空格）==
+app/BOOT-INF/classes/com/demo/NewService.class
!app/BOOT-INF/classes/com/demo/OldService.class
-app/BOOT-INF/classes/com/demo/Removed.class
?app/BOOT-INF/classes/com/demo/Ignore.class

== 形式 C：修改指令带新旧两个路径 ==
!app/BOOT-INF/classes/com/demo/OldService.class BOOT-INF/classes/com/demo/OldService.class

== 形式 D：三段路径 ==
!app/BOOT-INF/classes/com/demo/A.class BOOT-INF/classes com/demo/B.class

== 形式 E：无前缀，纯路径 ==
app/BOOT-INF/classes/com/demo/Plain.class

== 形式 F：斜杠开头 ==
+/abs/path/with/slash.class

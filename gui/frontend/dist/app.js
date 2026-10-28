/* PatchAssistant2J 前端。
 *
 * 直接用 window.go 调后端，**不引用 frontend/wailsjs 里生成的绑定** ——
 * 那个目录不在 frontend/dist 里，不经过打包器就取不到（会 404）。
 * 这里没有构建步骤，所以走运行时注入的 window.go。
 *
 * 视觉与交互之外的一切（匹配规则、写出格式）都在 Go 侧的 internal/service，
 * 与 CLI 共用同一套实现 —— 界面不重复实现任何业务规则。 */
(function () {
  'use strict';

  var $ = function (id) { return document.getElementById(id); };

  // ------------------------------------------------------------ 后端桥接

  var backendPromise = null;

  function backend() {
    if (backendPromise) return backendPromise;
    backendPromise = new Promise(function (resolve) {
      var tries = 0;
      var timer = setInterval(function () {
        if (window.go && window.go.main && window.go.main.App) {
          clearInterval(timer);
          resolve(window.go.main.App);
        } else if (++tries > 500) {          // 约 10 秒
          clearInterval(timer);
          throw new Error('后端未就绪：window.go 没有出现');
        }
      }, 20);
    });
    return backendPromise;
  }

  function call(method) {
    var args = Array.prototype.slice.call(arguments, 1);
    return backend().then(function (api) { return api[method].apply(api, args); });
  }

  // ---------------------------------------------------------------- 状态

  var state = {
    appPath: '', patchPath: '', appTree: null, patchTree: null, focusTree: 0
  };

  // ---------------------------------------------------------------- 提示

  var toastTimer = null;
  function toast(msg, isErr) {
    var el = $('toast');
    el.textContent = msg;
    el.className = 'toast' + (isErr ? ' err' : '');
    el.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { el.hidden = true; }, isErr ? 8000 : 3500);
  }

  /* working 对应原版的 isLoadingFlag()：正在读取/匹配时不再受理新的拖入。 */
  var working = false;
  function busy(on, msg) {
    working = !!on;
    $('st-msg').textContent = msg || '';
    document.body.style.cursor = on ? 'progress' : '';
  }

  // ------------------------------------------------------------ 树的渲染

  /* 展开状态与行索引。
   *
   * 为什么不能只靠 DOM：`apply()` 每次都会把整棵树重建一遍，DOM 上的展开态
   * 自然全丢。用户展开 `BOOT-INF/lib/xxx.jar` 就是为了看里面的高亮节点，
   * 结果右键复制个名字树就收起来了 —— 所以展开态要单独记着，重建后恢复。
   *
   * 键是 "<树>:<包内路径>"。加树前缀是因为应用树和补丁树的路径可能撞车
   * （两边的根下都可能有 README.txt）。 */
  var expanded = Object.create(null);
  var rows = Object.create(null);

  function clearExpanded(treeKey) {
    var prefix = treeKey + ':';
    Object.keys(expanded).forEach(function (k) {
      if (k.indexOf(prefix) === 0) delete expanded[k];
    });
  }

  function humanSize(n) {
    if (!n || n < 0) return '';
    if (n < 1024) return n + ' B';
    var u = ['KB', 'MB', 'GB'], i = -1;
    do { n /= 1024; i++; } while (n >= 1024 && i < u.length - 1);
    return n.toFixed(n < 10 ? 1 : 0) + ' ' + u[i];
  }

  function renderTree(host, root, hint, treeKey) {
    // 树重建后旧的行元素全失效（hits 里存的是 DOM 引用），搜索状态必须清掉
    var fst = fastSearch && fastSearch[treeKey];
    if (fst) fastSearchCancel(fst);

    host.textContent = '';
    rows = Object.create(null);
    // 行的 dom 路径索引也要跟着重建（见 makeRow / linksDraw）
    linksResetIndex(treeKey);
    var rootPath = root ? root.path : null;
    if (rootPath !== selRoot[treeKey]) {
      sel[treeKey] = null;                 // 换了包，旧的选中项作废
      selRoot[treeKey] = rootPath;
    }
    if (!root) {
      var d = document.createElement('div');
      d.className = 'empty';
      d.textContent = '未打开';
      if (hint) {
        var h = document.createElement('span');
        h.className = 'empty-hint';
        h.textContent = hint;
        d.appendChild(h);
      }
      host.appendChild(d);
      return;
    }
    host.appendChild(makeRow(root, 0, treeKey, DOM_ROOT));

    /* 重建后把选中态接回来。行是同步建出来的（makeRow 末尾会按 expanded
       把已展开的层重建），所以 links.index 里这时已经有了。
       接不回来（那一层要异步展开）就当没选中 —— 不能为了这个把树弄卡。 */
    var keep = sel[treeKey] && links.index[treeKey][sel[treeKey]];
    if (keep) keep.classList.add('selected');
  }

  /* makeRow 返回一个 DocumentFragment：一行 + （可选的）子级容器。
     子级是"展开时才建 DOM"，所以几千个条目的包也不会一次性铺满。

     两条与展开有关的规则：
      1. **压缩包节点即使现在没有 children 也算可展开**。嵌套 jar 是惰性展开的
         （对应原版双击节点时的 buildChildrenWithCompress），建树和匹配阶段
         只展开"用得上"的那些，其余在 JSON 里一个子级都没有。按"有 children
         才画三角"判的话，`BOOT-INF/lib/*.jar` 会变成死叶子 —— 用户点不开，
         藏在里面的东西（包括补丁命中的高亮节点）永远看不到。
      2. **点名字也能展开**，不必非得点那个小三角。 */
  /* 拖拽的"长按"时长（毫秒）。
     为什么要它：树行同时承担"点选中 / 双击预览"，一按就起拖的话手一抖就拖走，
     用户当场反馈"太容易误触"。按住这么久不动才算准备好，之后再移动才开始拖。 */
  var DRAG_ARM_MS = 300;

  /* parentRowOf 取一行的**父行**元素（行本身与它的子级容器是兄弟节点，
     见 makeRow 里 frag = [row, box] 的组装方式）。

     为什么不往上找最近的 .row：子行是装在 box 里的，而 box 与父行是兄弟，
     所以"祖先"里根本没有父行 —— 只能先回到 box 再取它的前一个兄弟。 */
  function parentRowOf(row) {
    var box = row.parentElement;
    if (!box) return null;
    var prev = box.previousElementSibling;
    return (prev && prev.classList && prev.classList.contains('row')) ? prev : null;
  }

  /* locOf 算一行在**定位串**里的写法：普通目录下就是节点自己的 Path；
     一旦父行是压缩包，就要补上 `父定位串!/` 前缀。

     为什么不能直接用 node.path：嵌套 jar 里节点的 Path 是**相对那个 jar**
     的（`com/xxl/job/core/biz/AdminBiz.class`），全树范围可能重名 ——
     两个 jar 里都有 `META-INF/MANIFEST.MF` 时，后端按 Path 查会命中第一个。
     拼成 `BOOT-INF/lib/x.jar!/com/...` 才是唯一的。 */
  function locOf(row) {
    if (!row || !row.__node) return '';
    var p = parentRowOf(row);
    if (p && p.__node && p.__node.compress) {
      return locOf(p) + '!/' + row.__node.path;
    }
    return row.__node.path;
  }

  function makeRow(node, depth, treeKey, dom) {
    var frag = document.createDocumentFragment();

    var kids = node.children || [];
    var key = treeKey + ':' + node.path;
    var openable = kids.length > 0 || !!node.compress;

    var row = document.createElement('div');
    row.className = 'row t-' + (node.type || 'none') + (openable ? ' expandable' : '');
    row.style.paddingLeft = (6 + depth * 13) + 'px';
    row.title = node.path || node.name;
    // 右键菜单要知道点的是哪个节点。树每次刷新都会重建 DOM，
    // 所以直接挂在元素上不会有陈旧引用的问题。
    row.__node = node;
    row.__tree = treeKey;
    /* dom 路径 = "从根往下第几个子节点"的链（`r`、`r/0/3`），与展开状态无关。
       匹配连线的两端就用它标识 —— 节点 Path 不行：嵌套 jar 里的条目 Path
       不带 jar 前缀，两个 jar 里都有 META-INF/MANIFEST.MF 时会撞车。
       规则必须与 Go 侧 collectDomPaths 完全一致。 */
    row.__dom = dom;
    linksIndexRow(treeKey, dom, row);

    /* 补丁树的行可以拖出去（补丁树 → 应用树）。
       只有这一个方向：原版 onDragOver 里 `originTree == 目标树` 直接 return，
       同树内拖动被明确禁止；`目标树 != appTree` 也直接 return，落点必须是
       应用树。
       **根行也能拖**：它代表"这个包"，拖过去＝把这个 jar/zip 按文件名放进
       目标目录（和从资源管理器拖一个 jar 进去同一条规则）。
       说明文件不拖：它不是要打进包里的内容，后端会拒（原版 isHasReadmeNode
       时整次拖放被拒），给个能拖的手感再弹报错不如干脆不给。

       **要按住一会儿才起拖**（用户 2026-10-28："太容易误触，改成长按才拖"）。
       为什么不改成"长按到了再把 draggable 设成 true"：Chrome 只在 mousedown
       那一刻判定元素可拖（Blink 的 mouse_down_may_start_drag_），中途改
       draggable 对本次手势无效。所以这里始终可拖，但在 dragstart 里检查
       "按够了没有"，没按够就 preventDefault 取消这次拖拽 —— dragstart 只在
       真的移动时才派发，于是"按住不动 → 够时长 → 再移动"就是长按起拖，
       而"一按就甩"会被挡掉。 */
    if (treeKey === 'patch' && node.type !== 'readme') {
      var armed = false;
      var armTimer = null;
      var disarm = function () {
        if (armTimer) { clearTimeout(armTimer); armTimer = null; }
        armed = false;
        row.classList.remove('drag-ready');
      };
      row.draggable = true;
      /* 长按这件事得说出来，否则谁也不会想到要按住不动 —— 顺手挂在悬停提示里。 */
      row.title = (row.title || '') + '\n按住约 0.3 秒后拖动，可放到左侧应用树';
      row.addEventListener('mousedown', function (e) {
        if (e.button !== 0) return;        // 只认左键
        if (armTimer) clearTimeout(armTimer);
        armTimer = setTimeout(function () {
          armTimer = null;
          armed = true;
          row.classList.add('drag-ready');   // 光标变抓手，告诉用户"现在可以拖了"
        }, DRAG_ARM_MS);
      });
      row.addEventListener('mouseup', disarm);
      row.addEventListener('mouseleave', disarm);
      row.addEventListener('dragstart', function (e) {
        if (!armed) {                       // 没按够 → 取消，防误触
          e.preventDefault();
          return;
        }
        dragFromTreeNode = locOf(row);
        dragFromTreeName = node.name || node.path;
        if (e.dataTransfer) {
          e.dataTransfer.effectAllowed = 'copy';
          // 塞个文本：拖到别处（比如输入框）时不至于完全没反应
          try { e.dataTransfer.setData('text/plain', node.name); } catch (err) {}
        }
      });
      row.addEventListener('dragend', disarm);
    }

    var tw = document.createElement('span');
    tw.className = 'twisty' + (openable ? '' : ' leaf');
    tw.textContent = openable ? (expanded[key] ? '\u25BE' : '\u25B8') : '';
    row.appendChild(tw);

    /* 文件类型图标。**用哪张图完全由后端决定**（node.icon，规则见
       gui/icon.go，逐条对齐原版 TreeNodeUtil.getIconImage + IConUtil）。
       这里只负责贴上去 —— 判定要看节点类型、路径、以及"根是不是 Spring Boot
       胖包"，那些前端都拿不到。 */
    var ico = document.createElement('img');
    ico.className = 'icon';
    ico.src = node.icon || 'icons/file.png';
    ico.alt = '';
    ico.draggable = false;
    row.appendChild(ico);

    /* 文件名这一格，以及跟在名字后面的两个小标记（「已修改」星号 /
       README 标签）—— 标记挂在 .name **里面**，不是挂在 row 上。
       为什么：.name 有 min-width 保底（见 app.css），挂在 row 上的话，
       短文件名会把标记推到 100px 处、跟名字之间空出一大截（实测空 63px）。 */
    var name = document.createElement('span');
    name.className = 'name';
    /* 纯文件名单独一层：省略号由它承担。
       若留在 .name 上，长文件名会把跟在后面的星号一起截掉。 */
    var nameText = document.createElement('span');
    nameText.className = 'name-text';
    // 目录名不带尾斜杠：原版靠图标区分目录与文件，斜杠是"没有图标列"时期的
    // 临时办法（见 app.css 的 .icon 注释）。图标补上后就不再需要了。
    nameText.textContent = node.name;
    name.appendChild(nameText);
    row.appendChild(name);

    /* 内容被改过的标记（右键「编辑内容」改的，见 MIGRATION.md 的
       「编辑包内文件内容」）。做成名字后面一个星号而**不是**改行底色：
       行底色已经被补丁状态（t-add/t-mod/t-del）占用了，那是"补丁对这个文件
       做了什么"，而这是"我改过它"，两个不同维度 —— 混在一个通道里就分不清了。 */
    if (node.edited) {
      var ed = document.createElement('span');
      ed.className = 'edited';
      ed.textContent = '*';
      ed.title = '内容已修改（只在本会话内有效，重新加载会丢弃）';
      name.appendChild(ed);
    }

    /* 被识别为说明文件的那个节点额外打个文字标签。
       原版只靠换成 readme.png 图标来区分 —— 那个图标我们现在也有了，所以
       这个标签是**多出来的**（当初就是因为"树没有图标列"才用文字代替）。
       留着是因为 16px 的图标夹在长文件名里不够显眼；嫌冗余删掉这一段即可，
       图标那侧不受影响。 */
    if (node.type === 'readme') {
      var tag = document.createElement('span');
      tag.className = 'tag';
      tag.textContent = 'README';
      name.appendChild(tag);
    }

    if (!node.dir) {
      if (node.modTime) {
        var mt = document.createElement('span');
        mt.className = 'modTime';
        mt.textContent = node.modTime;
        row.appendChild(mt);
      }
      var sz = document.createElement('span');
      sz.className = 'size';
      sz.textContent = humanSize(node.size);
      row.appendChild(sz);
      if (node.md5) {
        var md = document.createElement('span');
        md.className = 'md5';
        md.textContent = node.md5;
        /* 窄面板里 MD5 会被省略号截断（见 app.css 的 .md5），
           完整值挂 title 上 hover 可见。 */
        md.title = node.md5;
        row.appendChild(md);
      }
    }

    frag.appendChild(row);

    /* st 对**所有**行都建，哪怕它不可展开。
       原因是双击要打开内容预览（原版 HomeLeftTreeView/HomeRightTreeView
       的 onMouseClicked），而能打开预览的恰恰多是"不可展开的叶子文件"。
       早先的版本在 `!openable` 时直接 return，结果普通文件双击毫无反应。 */
    var st = {
      node: node, depth: depth, treeKey: treeKey, key: key,
      box: null, tw: tw, built: false, loading: false, kids: kids,
      toggle: function () {}   // 叶子没有可展开的东西，默认空操作
    };
    rows[key] = st;
    /* 定位要沿 DOM 逐层往下走（见 revealRow），所以把 st 也挂在行元素上。
       不能靠 rows 索引：它是按 path 建的键，而嵌套 jar 内部子节点的 Path
       **不带 jar 前缀**（上游行为，见 MIGRATION.md §五），全树范围内可能
       重名 —— 查索引会走到别的 jar 里去。 */
    row.__st = st;

    /* 定位：单击补丁树里**有对应项的文件行**，去应用树里展开并定位过去。
       —— 原版没有这个功能，属刻意增强（见 MIGRATION.md §一）。

       只挂给文件，目录不挂：点目录的语义就是展开。带内部类的 .class、
       嵌套 jar 这些"可展开的文件"照挂不误 —— 它们本来就还有一个展开动作，
       定位是额外一层信息，两者不冲突。

       用和"点名字展开"同一个 200ms 延迟躲开双击：双击是打开内容预览，
       不延迟的话双击会先触发一次定位。 */
    if (treeKey === 'patch' && !node.dir && node.mapped) {
      row.addEventListener('click', function () {
        clearTimeout(pendingLocate);
        pendingLocate = setTimeout(function () { locateCounterpart(treeKey, node); }, 200);
      });
      row.addEventListener('dblclick', function () { clearTimeout(pendingLocate); });
    }

    if (!openable) {
      row.addEventListener('dblclick', function () { onActivate(st); });
      return frag;
    }

    var box = document.createElement('div');
    box.hidden = true;
    frag.appendChild(box);
    st.box = box;

    function openDom() {
      box.hidden = false;
      tw.textContent = '\u25BE';
    }

    function buildKids() {
      var f = document.createDocumentFragment();
      for (var i = 0; i < st.kids.length; i++) {
        f.appendChild(makeRow(st.kids[i], depth + 1, treeKey, dom + '/' + i));
      }
      box.textContent = '';
      box.appendChild(f);
      st.built = true;
      openDom();
      // 新行出现了，连线要补画（配对可能就在这些行里）
      linksSchedule();
    }

    /* toggle(true) 强制展开（已经展开则什么都不做）；
       不带参数就是普通的开/关。 */
    st.toggle = function (forceOpen) {
      var isOpen = !box.hidden;
      if (forceOpen === true) {
        if (isOpen) return;
      } else if (isOpen) {
        delete expanded[key];
        box.hidden = true;
        tw.textContent = '\u25B8';
        linksSchedule();          // 收起后那些行不可见了，连线要跟着收
        return;
      }
      expanded[key] = true;
      if (st.built) { openDom(); linksSchedule(); return; }
      if (st.kids.length > 0) { buildKids(); return; }
      if (!node.compress || st.loading) return;

      // 压缩包还没有子级：找后端现场展开（对应原版双击节点时的
      // buildChildrenWithCompress + buildNodeChildren）。
      st.loading = true;
      tw.textContent = '\u22EF';
      call('ExpandNode', treeKey, node.path)
        .then(function (v) {
          st.loading = false;
          // 后端回的是这个节点展开后的视图，直接拿它的子级
          node.children = v.children || [];
          node.compress = !!v.compress;
          st.kids = node.children;
          if (st.kids.length === 0) {
            // 空压缩包：标记成"打开过"，但没什么可显示
            openDom();
            toast('这个压缩包里没有条目');
            return;
          }
          buildKids();
        })
        .catch(function (e) {
          st.loading = false;
          delete expanded[key];
          tw.textContent = '\u25B8';
          toast(errText(e), true);
        });
    };

    // 三角：立刻响应，不做防抖 —— 它本来就是明确的展开控件
    tw.addEventListener('click', function (e) { e.stopPropagation(); st.toggle(); });
    tw.addEventListener('dblclick', function (e) { e.stopPropagation(); });

    /* 名字：单击展开/收起。
       双击要留给"打开内容"（原版 HomeLeftTreeView/HomeRightTreeView 的
       onMouseClicked 就是双击），所以单击延后 200ms 再执行 —— 双击时
       把待执行的单击取消掉，两种手势就不会互相打架。 */
    name.addEventListener('click', function () {
      clearTimeout(pendingClick);
      pendingClick = setTimeout(function () { st.toggle(); }, 200);
    });

    /* 双击：取消上面那个待执行的单击，再走激活。
       不取消的话，双击展开目录会"先展开再收起"（第二次点击的定时器
       在 dblclick 之后才到）。 */
    row.addEventListener('dblclick', function () {
      clearTimeout(pendingClick);
      onActivate(st);
    });

    // 重建后恢复展开态
    if (expanded[key]) st.toggle(true);

    return frag;
  }

  var pendingClick = null;
  // 定位的延迟定时器。和 pendingClick 分开：单击补丁树里"可展开的文件"时
  // 两个动作都要跑（展开 + 定位），共用一个定时器会互相取消。
  var pendingLocate = null;

  /* onActivate 对应原版双击节点的行为：
       - 压缩包（jar/zip）-> 展开它
       - 目录            -> 展开它（原版这里是弹 ContentView，而 showDialog
                            开头 `if (leftNode.isDir()) return` 直接返回，
                            等于没反应；我们既然已经把展开挪到单击上，
                            双击保持"展开"更自然）
       - 说明文件        -> 原版弹 README 编辑器，我们还没移植，改为看内容
       - 其他文件        -> 打开内容对话框（ContentView）
     见 MIGRATION.md §五。 */
  function onActivate(st) {
    var node = st.node;
    if (node.compress || node.dir) {
      st.toggle(true);
      return;
    }
    openContent(st.treeKey, node);
  }

  // ------------------------------------------------------- 在另一棵树里定位

  /* locateCounterpart 在另一棵树里定位这个节点的对应项。
     对应关系与祖先链都由后端给（见 gui/app.go 的 LocateMapped）——
     前端既拿不到 MappedNode，也算不出嵌套 jar 内部的路径。 */
  function locateCounterpart(treeKey, node) {
    call('LocateMapped', treeKey, node.path)
      .then(function (r) {
        if (!r) return;
        /* 先试着在现有 DOM 上直接定位。定位十有八九只是"展开 + 滚过去"，
           树本身没变，能直接定位就别重建 —— apply() 会把两棵树全拆了重画，
           选中态、滚动位置、展开的嵌套 jar 全要重来一遍，为了看一眼不值当。
           而且重建会把刚点上的 .selected 清掉，连线就不加粗了。 */
        var row = revealRow(r.side, r.ancestors || [], r.path);
        if (!row) {
          // 祖先链有变化（后端现场解压了嵌套 jar），只能整体重渲染
          apply(r.state);
          row = revealRow(r.side, r.ancestors || [], r.path);
          if (!row) {
            toast('没能定位到对应项，重新匹配一下试试', true);
            return;
          }
        }
        flashRow(row);
        /* 对端也选中：两端都亮着，中间那条连线才是加粗的 ——
           "我点的这个文件在对面哪个位置"一句话说完。 */
        setSelectedRow(r.side === 'app' ? $('app-tree') : $('patch-tree'), row);
      })
      .catch(function (e) { toast(errText(e), true); });
  }

  /* revealRow 依次展开祖先，返回目标那一行（找不到返回 null）。

     **沿 DOM 逐层往下走，而不是查 rows 索引。** rows 是按 path 建的键，而
     嵌套 jar 内部子节点的 Path 不带 jar 前缀（上游行为），全树范围内可能
     重名 —— 查索引会把别的 jar 里的同名目录一起展开。沿 DOM 走就天然限定在
     正确的父级下面。

     每一步的 toggle(true) 都是同步的：后端已经保证沿途祖先都有子级
     （映射存在就说明匹配阶段下探过），所以 st.kids 非空，直接建 DOM。 */
  function revealRow(treeKey, ancestors, targetPath) {
    var host = treeKey === 'app' ? $('app-tree') : $('patch-tree');
    if (!host) return null;
    var row = host.querySelector('.row');   // 根那一行
    if (!row || !row.__st) return null;

    row.__st.toggle(true);                  // 根先展开，第一层才存在
    for (var i = 0; i < ancestors.length; i++) {
      row = childRowByPath(row.__st, ancestors[i]);
      if (!row || !row.__st) return null;
      row.__st.toggle(true);                // 展开这一层，下一层才会被建出来
    }
    return childRowByPath(row.__st, targetPath);
  }

  /* childRowByPath 在某个已展开的节点里，按路径找它的**直接**子行。 */
  function childRowByPath(st, path) {
    if (!st || !st.box) return null;
    var kids = st.box.children;
    for (var i = 0; i < kids.length; i++) {
      var r = kids[i];
      if (r.__node && r.__node.path === path) return r;
    }
    return null;
  }

  /* flashRow 把定位到的行滚进视野并短暂高亮。

     高亮是**短暂的**、不做持久选中：树会因为重新匹配整个换掉，持久高亮
     可能指到另一个节点上去。树本身展开了、滚过去了，注意力已经带到位。 */
  var locateTimer = null;
  function flashRow(row) {
    var prev = document.querySelector('.row.located');
    if (prev) prev.classList.remove('located');
    row.classList.add('located');
    row.scrollIntoView({ block: 'center', inline: 'nearest' });
    clearTimeout(locateTimer);
    locateTimer = setTimeout(function () { row.classList.remove('located'); }, 3000);
  }

  /* revealByPath 按"包内路径"定位并高亮一个节点。
   *
   * 与 revealRow 的分工：revealRow 要**祖先链**（dom 路径），而这里只有一个
   * 业务路径（如 `BOOT-INF/classes/com/example/Foo.class`）。所以先问后端要
   * 祖先链（LocateNode），再交给 revealRow 逐层展开。
   *
   * 用在"忘了路径"的拖放匹配之后：命中/新增完把那个文件滚到眼前并闪一下，
   * 用户立刻看到它落在哪个目录 —— 否则替换成功了却不知道替换到哪儿去了。
   *
   * 返回 Promise<boolean>：true 表示确实滚到并选中了。绝大多数调用方不看这个
   * 返回值（定位失败不该影响已经完成的替换），但"先看再替换"那条路要看 ——
   * 没定位到就等于用户没看见，得如实告诉他，不能让他盲着点替换。 */
  function revealByPath(side, path) {
    if (!path) return Promise.resolve(false);
    return call('LocateNode', side, path).then(function (r) {
      if (!r) return false;
      var row = revealRow(r.side, r.ancestors || [], r.path);
      if (!row && r.state) {          // 祖先还没建出来（嵌套 jar 现场展开过）
        apply(r.state);
        row = revealRow(r.side, r.ancestors || [], r.path);
      }
      if (!row) return false;
      flashRow(row);
      setSelectedRow(r.side === 'app' ? $('app-tree') : $('patch-tree'), row);
      return true;
    }).catch(function () { return false; });   // 定位失败不影响已经完成的替换
  }

  /* -------------------------------------------------- 临时补丁（标记删除清单）

     Go 版新增，原版没有。数据来自 State.manualDelList —— 后端扫应用树
     收集所有被标成 DEL 的文件。这里把它们渲染成补丁树面板顶部的清单，
     每行可点：滚到应用树里那一项并高亮，方便删多了之后核对。

     **显示完整路径而不是文件名** —— 一个 jar 里同名文件太常见了
     （META-INF/build-info.properties 和 BOOT-INF/classes/build-info.properties
     在 Spring Boot 包里并存），只给文件名等于白给。文件名放在 title 里，
     鼠标悬停能看（但主要靠路径辨识）。

     为什么不用 Backbone/框架：整个前端是零依赖的裸 JS，这也一样。
     列表重建即可 —— 条数通常几十条，重建成本远高于做 diff。 */
  function renderTempPatch(list) {
    var box = $('temp-patch');
    var ul = $('temp-patch-list');
    if (!box || !ul) return;
    list = list || [];
    box.hidden = list.length === 0;
    $('temp-patch-count').textContent = list.length;
    ul.textContent = '';

    list.forEach(function (e) {
      var li = document.createElement('li');
      li.title = e.name + '\n' + e.path;
      var name = document.createElement('span');
      name.className = 'tp-name';
      // 路径优先；后端兜底保证非空，这里再兜一次
      name.textContent = e.path || e.name || '?';
      li.appendChild(name);
      if (typeof e.size === 'number' && e.size > 0) {
        var sz = document.createElement('span');
        sz.className = 'tp-size';
        sz.textContent = fmtSize(e.size);
        li.appendChild(sz);
      }
      li.addEventListener('click', function () { locateAppNode(e.path); });
      ul.appendChild(li);
    });
  }

  /* locateAppNode 在应用树里定位并高亮一个路径。
     走 LocateNode 而不是 LocateMapped —— 标删的节点没有映射对端，
     后者会直接报"没有对应项"。 */
  function locateAppNode(path) {
    call('LocateNode', 'app', path)
      .then(function (r) {
        apply(r.state);
        var row = revealRow(r.side, r.ancestors || [], r.path);
        if (!row) {
          toast('没能定位到该项', true);
          return;
        }
        flashRow(row);
      })
      .catch(function (e) { toast(errText(e), true); });
  }

  /* fmtSize 按 1024 进制给个人类可读的体积。 */
  function fmtSize(n) {
    if (n < 1024) return n + ' B';
    var units = ['KB', 'MB', 'GB'];
    var v = n / 1024, i = 0;
    while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
    return (v < 10 ? v.toFixed(1) : Math.round(v)) + ' ' + units[i];
  }

  // --------------------------------------------------------- 树上打字即搜

  /* 对应原版 `view/home/FastSearchControl`：**在树上直接打字**就浮出一个输入框，
     增量匹配并选中离起点最近的那一行。

     必须对齐原版的几个点：
       1. 搜的范围是**已展开的节点** —— 原版只把 `isExpanded()` 的子级入队。
          Go 版 DOM 里渲染出来的行恰好就是已展开的部分，所以直接遍历 `.row`。
       2. 匹配是 `name.contains(keyword)`，**区分大小写**（原版用的就是 contains）。
       3. 选中目标是"离开始搜索时那一行最近的结果"；没有选中行时取第一个。
       4. `Backspace` 删一个字符、`Esc` 取消、`Ctrl+Alt+↑/↓` 切上一个/下一个。
       5. 输入框宽度随文本变化（原版 `TextDynamicWidthField`）。

     **一处刻意修正**（已登记在 MIGRATION.md）：原版 `selectPre` / `selectNext`
     取元素时用的是 `curIndex.getAndUpdate(...)` 的返回值，而 `getAndUpdate` 返回的是
     **旧值** —— 于是两个方法取到的是同一个元素（只有内部游标不同），"上一个"按下去
     显示的是当前项的下一项。这里按语义修正成"↑ 前一个 / ↓ 后一个"。 */
  var fastSearch = Object.create(null);

  function fastSearchCancel(st) {
    if (!st) return;
    st.keyword = '';
    st.hits = [];
    st.index = 0;
    st.active = false;
    st.field.value = '';
    st.field.hidden = true;
  }

  function fastSearchRows(st) {
    return Array.prototype.slice.call(st.host.querySelectorAll('.row'));
  }

  /* 选中一行。原版每次都是 clearSelection + select（TreeView 的选中是唯一的），
     这里等价地维护一个 `.row.selected`。 */
  /* 每棵树当前选中的行（dom 路径）。
     树重渲染会把 .selected 一起清掉，而"点补丁文件 → 定位到应用树"这条路
     正好会重渲染 —— 清掉之后连线就没东西可加粗了（用户报的"点了不高亮"）。
     所以把选中项记下来，重建完再接回去。 */
  var sel = { app: null, patch: null };
  /* 上次渲染的根路径。根变了说明换了包，旧 dom 路径已经没有意义，不能接回去。 */
  var selRoot = { app: null, patch: null };

  function setSelectedRow(host, row) {
    var prev = host.querySelector('.row.selected');
    if (prev && prev !== row) prev.classList.remove('selected');
    if (row) {
      row.classList.add('selected');
      sel[row.__tree] = row.__dom;
    }
    // 选中换人 → 加粗的连线也要换
    linksSchedule();
  }

  function fastSearchPlace(st) {
    st.field.style.top = (st.host.offsetTop + 2) + 'px';
    st.field.style.left = (st.host.offsetLeft + 4) + 'px';
  }

  /* 宽度随文本变化。原版量的是 JavaFX Text 的 layoutBounds，
     浏览器里用同字体的离屏 span 量，再加左右内边距与边框。 */
  function fastSearchResize(st, keyword) {
    st.measure.textContent = keyword;
    st.field.style.width = Math.max(60, st.measure.offsetWidth + 18) + 'px';
  }

  /* 选"离起点最近的结果"，逐条对齐原版 search() 里的判据：
       优先 curDistance == 0（就是起点那一行）；
       其次优先下方（curDistance < 0）且当前还是上方/无穷远；
       最后按绝对距离最近。
     firstIndex 为 -1（没有选中行）时等价于"取第一个结果"。 */
  function fastSearchTarget(st, rowIdx) {
    var first = st.firstIndex;
    var distance = Infinity;
    var target = 0;
    for (var i = 0; i < rowIdx.length; i++) {
      var cur = first - rowIdx[i];
      if (cur === 0) { distance = cur; target = i; }
      else if (cur < 0 && distance > 0) { distance = cur; target = i; }
      else if (!(cur > 0 && distance < 0) && Math.abs(cur) < Math.abs(distance)) {
        distance = cur; target = i;
      }
    }
    return target;
  }

  function fastSearchSelect(st) {
    if (st.index < 0 || st.index >= st.hits.length) return;
    var row = st.hits[st.index];
    setSelectedRow(st.host, row);
    row.scrollIntoView({ block: 'nearest' });
  }

  function fastSearchRun(st, keyword) {
    var all = fastSearchRows(st);
    var hits = [], idx = [];
    for (var i = 0; i < all.length; i++) {
      var nt = all[i].querySelector('.name-text');
      if (nt && nt.textContent.indexOf(keyword) >= 0) { hits.push(all[i]); idx.push(i); }
    }
    st.hits = hits;
    st.index = hits.length ? fastSearchTarget(st, idx) : -1;
    fastSearchSelect(st);
  }

  /* 输入变化（树上打字与直接编辑输入框都走这里）。 */
  function fastSearchInput(st, keyword) {
    if (keyword === '') { fastSearchCancel(st); return; }
    if (!st.active) {
      // 首次进入搜索：记下起点行，对应原版 firstSelectIndex
      var sel = st.host.querySelector('.row.selected');
      st.firstIndex = sel ? fastSearchRows(st).indexOf(sel) : -1;
      st.active = true;
      st.field.hidden = false;
      fastSearchPlace(st);
    }
    st.keyword = keyword;
    st.field.value = keyword;
    fastSearchResize(st, keyword);
    fastSearchRun(st, keyword);
  }

  function fastSearchStep(st, delta) {
    var n = st.hits.length;
    if (!n) return;
    st.index = ((st.index + delta) % n + n) % n;
    fastSearchSelect(st);
  }

  /* 搜索激活时才吃的两个组合键，树与输入框共用。返回 true 表示已处理。 */
  function fastSearchHotkey(st, e) {
    if (!st.active) return false;
    if (e.ctrlKey && e.altKey && (e.key === 'ArrowUp' || e.key === 'ArrowDown')) {
      fastSearchStep(st, e.key === 'ArrowUp' ? -1 : 1);
      e.preventDefault(); e.stopPropagation();
      return true;
    }
    if (e.key === 'Escape') {
      fastSearchCancel(st);
      e.preventDefault(); e.stopPropagation();
      return true;
    }
    return false;
  }

  function initFastSearch(host, treeKey) {
    var pane = host.parentNode;
    // 树要能拿到键盘焦点，否则"在树上打字"没人接（原版 TreeView 本身可聚焦）
    host.tabIndex = 0;

    var field = document.createElement('input');
    field.className = 'fastsearch';
    field.type = 'text';
    field.hidden = true;
    field.setAttribute('aria-label', '快速搜索');
    pane.appendChild(field);

    var measure = document.createElement('span');
    measure.className = 'fastsearch-measure';
    pane.appendChild(measure);

    var st = {
      treeKey: treeKey, host: host, field: field, measure: measure,
      keyword: '', hits: [], index: 0, firstIndex: -1, active: false
    };
    fastSearch[treeKey] = st;

    // div 不会自动聚焦，点一下树要手动把焦点收回来
    host.addEventListener('mousedown', function () { host.focus(); });
    // 点行记为选中（原版 TreeView 有 selection model，Go 版补上这个视觉态）
    host.addEventListener('click', function (e) {
      var row = e.target && e.target.closest ? e.target.closest('.row') : null;
      if (row && host.contains(row)) setSelectedRow(host, row);
    });

    /* 树上打字。原版是 `setOnKeyTyped(textField::fireEvent)` ——
       把按键转发给那个隐藏输入框，由输入框的 textProperty 驱动搜索。 */
    host.addEventListener('keydown', function (e) {
      if (st.active && e.key === 'Backspace') {
        fastSearchInput(st, st.keyword.slice(0, -1));
        e.preventDefault();
        return;
      }
      if (fastSearchHotkey(st, e)) return;
      if (e.ctrlKey || e.altKey || e.metaKey) return;
      if (e.key.length !== 1) return;
      fastSearchInput(st, st.keyword + e.key);
      e.preventDefault();
    });

    /* 输入框本身也可编辑 —— 这样输入法（中文）能用：IME 的组合输入不会产生
       单字符 keydown，只能靠输入框的 input 事件拿到结果。 */
    field.addEventListener('input', function () { fastSearchInput(st, field.value); });
    field.addEventListener('keydown', function (e) { fastSearchHotkey(st, e); });
  }

  // ---------------------------------------------------------------- 全局搜索

  /* 对应原版 view/search/SearchView + HomeView 的 Ctrl+F / 工具栏搜索按钮。

     搜索本身在**后端**做（见 gui/search.go）—— 原版遍历的是内存里的树，
     而且路径式搜索会为了探进嵌套 jar 而现场解压；前端只有一份懒渲染的 DOM，
     既看不到全部节点也没有解压能力。这里只负责"发关键词、画结果、跳转"。 */

  var search = { open: false, items: [], token: 0, deepTimer: 0 };

  /* token 就是原版的 lastVer：每次输入都 +1，回调回来时若已经不是最新
     那一次就直接丢弃 —— 打字快的时候后端可能乱序返回。原版也是这个套路，
     即时搜索没有做输入防抖（防抖会拖慢"边打边出结果"的手感）。

     deepTimer 是**兜底深度搜**的防抖计时器，见 searchRun 里的说明。 */
  function searchRun(keyword) {
    var token = ++search.token;
    clearTimeout(search.deepTimer);            // 新输入：取消还没发出去的深度搜
    return call('SearchNodes', keyword).then(function (items) {
      if (token !== search.token) return;
      search.items = items || [];
      searchRender(search.items);
      /* 普通搜一条都没中 → 自动兜底：把没展开的嵌套 jar 也扫一遍。
         普通搜只搜"已经加载进树的节点"，所以嵌在 jar 里的文件（Spring Boot 的
         BOOT-INF/lib/*.jar 尤其多）看不见。停手 500ms 后再触发一次深度搜 ——
         必须防抖：即时搜是每敲一键调一次，不防抖的话打一行文件名会连着触发
         十几次全量扫 jar（实测一次约 2.8s）。路径式关键词不触发，它自己就会
         沿途展开嵌套 jar。 */
      if (keyword && search.items.length === 0 && !hasPathSep(keyword)) {
        search.deepTimer = setTimeout(function () { searchDeepRun(keyword, token); }, 500);
      }
    }).catch(function (e) {
      if (token !== search.token) return;
      toast(errText(e), true);
    });
  }

  function hasPathSep(kw) { return kw.indexOf('/') >= 0 || kw.indexOf('\\') >= 0; }

  /* searchDeepRun 兜底深度搜（后端 SearchNodesDeep）：展开所有没展开的嵌套 jar
     再搜一遍。命中就渲染结果；还是没有就给一句"已包含嵌套 jar"的提示，
     免得用户以为工具没找全。 */
  function searchDeepRun(keyword, token) {
    searchRenderMessage('正在搜索嵌套包…（包较大时需要几秒）');
    call('SearchNodesDeep', keyword).then(function (items) {
      if (token !== search.token) return;
      search.items = items || [];
      if (search.items.length === 0) {
        searchRenderMessage('没有找到（已包含嵌套 jar 内的文件）');
      } else {
        searchRender(search.items);
      }
    }).catch(function (e) {
      if (token !== search.token) return;
      toast(errText(e), true);
    });
  }

  function searchRender(items) {
    var list = $('search-list');
    if (!list) return;
    list.textContent = '';
    for (var i = 0; i < items.length; i++) list.appendChild(searchRow(items[i]));
  }

  /* searchRenderMessage 在结果区显示一行居中提示（"正在搜…" / "没找到…"）。
     不是结果行，所以不带 .search-row（走查数结果条数时不会把它算进去）。 */
  function searchRenderMessage(text) {
    var list = $('search-list');
    if (!list) return;
    list.textContent = '';
    var d = document.createElement('div');
    d.className = 'search-msg';
    d.textContent = text;
    list.appendChild(d);
  }

  /* searchRow 画一行。字段与顺序逐条对齐原版 SearchListCell.updateItem：
     名称（title）→ 路径（detail）→ 撑开的 Region → 图标。
     历史项多一个时间标签，且图标是 time.png；节点项是节点自己的图标。 */
  function searchRow(it) {
    var row = document.createElement('div');
    row.className = 'search-row';
    // 走查要能读到这一项背后的字段（祖先链等），挂在元素上最直接
    row.__item = it;

    var name = document.createElement('span');
    name.className = 's-name';
    name.textContent = it.name || '';
    row.appendChild(name);

    var path = document.createElement('span');
    path.className = 's-path';
    path.textContent = it.path || '';
    path.title = it.path || '';
    row.appendChild(path);

    var gap = document.createElement('span');
    gap.className = 'grow';
    row.appendChild(gap);

    if (it.history) {
      if (it.searchTime) {
        var tm = document.createElement('span');
        tm.className = 's-time';
        tm.textContent = it.searchTime;
        row.appendChild(tm);
        row.appendChild(searchIcon(it.icon || 'icons/time.png'));
      }
    } else {
      row.appendChild(searchIcon(it.icon || 'icons/file.png'));
    }

    /* 原版 SearchListCell 是"整个格子一个 onMouseClicked"，按 getClickCount
       分派。DOM 这边 click / dblclick 是分开的两个事件，但语义能对上：
       双击时浏览器会先发两次 click 再发 dblclick，而 click 分支只对
       **历史项**有效、dblclick 只对**节点项**有效，所以互不干扰。 */
    row.addEventListener('click', function () {
      // 单击历史项 → 把关键词灌回搜索框（会连带触发一次新的搜索）
      if (it.history) searchFill(it.name || '');
    });
    row.addEventListener('dblclick', function () {
      if (!it.history) searchGoTo(it);
    });
    return row;
  }

  function searchIcon(src) {
    var img = document.createElement('img');
    img.className = 's-icon';
    img.src = src;
    img.alt = '';
    img.draggable = false;
    return img;
  }

  /* searchFill 对应原版 `view.searchField.setText(item.getName())`。
     setText 会触发 textProperty 监听 → 重新搜一次，这里手动补上这一步。 */
  function searchFill(text) {
    var inp = $('search-input');
    if (!inp) return;
    inp.value = text;
    searchRun(text);
  }

  /* searchGoTo 双击结果项 → 在对应的树里选中并滚过去。
     对应原版 SearchListCell 里 getClickCount()==2 那一段：
     clearSelection → select → scrollTo → 写历史 → 关对话框。 */
  function searchGoTo(it) {
    // 写历史用的是**搜索框当前的文本**（不是被点的那一项），与原文一致
    var keyword = $('search-input') ? $('search-input').value : '';
    call('AddSearchHistory', keyword, it.nodePath).catch(function () {});

    searchClose();

    /* 路径式搜索可能在后端**现场解压**过嵌套 jar（原版也一样，并且会把
       解压结果 buildNodeChildren 到界面上）。所以先重取一次状态再定位，
       否则要展开的那些层级在前端的旧数据里根本不存在。 */
    call('GetState').then(function (s) {
      apply(s);
      var host = it.side === 'patch' ? $('patch-tree') : $('app-tree');
      var row = revealRow(it.side, it.ancestors || [], it.nodePath);
      if (!row) {
        toast('没能定位到该节点，重新匹配一下试试', true);
        return;
      }
      setSelectedRow(host, row);
      row.scrollIntoView({ block: 'nearest', inline: 'nearest' });
    }).catch(function (e) { toast(errText(e), true); });
  }

  function searchOpen() {
    var ov = $('search-overlay');
    if (!ov) return;
    search.open = true;
    ov.hidden = false;
    var inp = $('search-input');
    /* 原版每次都是 `new SearchView(this).showDialog(stage)` —— 全新实例，
       搜索框是空的，于是"打开就展示搜索历史"。这里等价地清空再搜，
       否则上一次的关键词会留着，重开看到的是上一次的结果而不是历史。 */
    inp.value = '';
    inp.focus();
    searchRun('');
  }

  function searchClose() {
    var ov = $('search-overlay');
    if (ov) ov.hidden = true;
    search.open = false;
    // 作废在途的搜索：关掉之后再回来的结果不该再往列表里塞
    search.token++;
    // 还没发出去的兜底深度搜也别发了（省掉那 2.8s 的全量扫 jar）
    clearTimeout(search.deepTimer);
  }

  function initSearch() {
    var ov = $('search-overlay');
    if (!ov) return;
    $('btn-search').addEventListener('click', searchOpen);
    $('search-x').addEventListener('click', searchClose);
    $('search-close').addEventListener('click', searchClose);
    /* 点遮罩**不关**弹窗 —— 见 init 里确认框那段的说明。 */

    // 输入即搜（原版 searchField.textProperty().addListener）
    $('search-input').addEventListener('input', function () { searchRun(this.value); });

    /* Ctrl+F —— 原版挂在 HomeView 的 rootPane 上（主窗口级）。
       内容预览面板里的 Ctrl+F 是"面板内查找"，那个处理器挂在 #content-win 上
       并且会 stopPropagation，所以焦点在预览面板里时事件到不了这里，
       两边不会打架 —— 与原版"主窗口 / 内容对话框各管各的"一致。 */
    document.addEventListener('keydown', function (e) {
      if (!e.ctrlKey || e.altKey || e.metaKey) return;
      if (e.key !== 'f' && e.key !== 'F') return;
      e.preventDefault();
      if (search.open) {
        $('search-input').focus();
        $('search-input').select();
        return;
      }
      searchOpen();
    });
  }

  // ------------------------------------------------------------ 状态刷新

  /* 对齐 Java StringUtil.isBlank：空串，或**每个字符都是"空格类字符"**才算空。
     坑在 Java 用的是 Character.isSpaceChar —— 它只认 Unicode 的 Zs/Zl/Zp 三类，
     `\n` / `\t` 这类控制字符不算。所以一份内容全是换行的 README 在原版眼里
     **不算空**，会被当成正文显示出来。照搬这个口径，别顺手换成 trim()。
     详见 MIGRATION.md §3.2。 */
  function isBlank(v) {
    if (v == null || v === '') return true;
    for (var i = 0; i < v.length; i++) {
      if (!/[\p{Zs}\p{Zl}\p{Zp}]/u.test(v[i])) return false;
    }
    return true;
  }

  /* patchInfoText 拼出底部「补丁信息」面板的正文。
     逐条对齐 Java HomeView.updatePatchInfo —— 面板里最多两段，顺序固定：
     「警告」（补丁比对提示）在前、「说明」（README 正文）在后，两段之间空一行。
     两段都没有就留空（原版是 setText(null)）。 */
  function patchInfoText(s) {
    var warn = s.diffInfo, readme = s.readmeText;
    var hasApp = !!s.appTree, hasPatch = !!s.patchTree;

    if (hasApp && hasPatch) {
      if (isBlank(warn)) return isBlank(readme) ? '' : '说明：\n' + readme;
      if (isBlank(readme)) return '警告：\n' + warn;
      return '警告：\n' + warn + '\n\n说明：\n' + readme;
    }
    if (hasApp) return isBlank(warn) ? '' : '警告：\n' + warn;
    if (hasPatch) return isBlank(readme) ? '' : '说明：\n' + readme;
    return '';
  }

  function apply(s) {
    if (!s) return;
    // 换包了就把那一侧的展开态清掉 —— 新包的路径跟旧包对不上，
    // 留着只会让恢复逻辑去展开一堆不存在的路径。
    if (state.appPath !== (s.appPath || '')) { clearExpanded('app'); clearPending(); }
    if (state.patchPath !== (s.patchPath || '')) clearExpanded('patch');

    state.appPath = s.appPath || '';
    state.patchPath = s.patchPath || '';
    state.appTree = s.appTree || null;
    state.patchTree = s.patchTree || null;

    $('app-path').textContent = s.appPath || '未打开';
    $('patch-path').textContent = s.patchPath || '未打开';

    renderTree($('app-tree'), s.appTree, '把 .jar 拖到这里', 'app');
    renderTree($('patch-tree'), s.patchTree, '把 .jar / .zip 或目录拖到这里', 'patch');
    // 匹配连线的两端清单（设置里关掉连线时后端给的是空数组）
    linksSet(s.mappedLinks);

    var t = s.total || { add: 0, mod: 0, del: 0, manualDel: 0 };
    $('st-add').textContent = t.add;
    $('st-mod').textContent = t.mod;
    $('st-del').textContent = t.del;

    var a = s.appStats || {}, p = s.patchStats || {};
    $('st-app').textContent = s.appTree
      ? '应用包 ' + a.files + ' 文件 / ' + a.dirs + ' 目录 / ' + a.classes + ' class'
      : '应用包 —';
    $('st-patch').textContent = s.patchTree
      ? '补丁包 ' + p.files + ' 文件 / ' + p.dirs + ' 目录'
      : '补丁包 —';

    if (s.settings) fillSettings(s.settings);

    // 聚焦：0 都显示 / 1 只看应用树 / 2 只看补丁树。
    // 对应原版 HomeView.focusTree —— 它是把另一个面板从 SplitPane 里摘掉，
    // 这里等价地隐藏那个面板。
    state.focusTree = s.focusTree || 0;
    document.body.classList.toggle('focus-app', state.focusTree === 1);
    document.body.classList.toggle('focus-patch', state.focusTree === 2);

    /* 「执行打补丁」只要有应用树就能点 —— **不再要求补丁包**。
       后端 SaveAppPack 本身支持 PatchTreeInfo=nil（"不打补丁、只是重打包"），
       而"只打开一个 jar、标删几个文件、输出新包"是最常见的用法之一。
       「重新匹配」仍然要求两侧都在（没有补丁包就无从匹配）。 */
    $('btn-run').disabled = !s.appTree;
    $('btn-refresh').disabled = !(s.appTree && s.patchTree);

    renderTempPatch(s.manualDelList);

    $('info-body').textContent = patchInfoText(s);
  }

  // ------------------------------------------------------------ 确认框

  /* confirmBox 是原生 window.confirm 的替身。
   *
   * 为什么不用原生的：WebView2 里它顶一条「wails.localhost 显示」的标题栏、
   * 按钮是系统样式，跟界面完全两个世界（用户提的）；而且它是**同步阻塞**的，
   * 弹着的时候整页都停住，连进度条都不动。这里做成 Promise，调用方
   * `.then(function (ok) { if (!ok) return; ... })` 就行。
   *
   * opts: { title, text, okText, cancelText, hint, danger }
   *   danger 为真时确定键换成红色（删除、撤销删除这类不可逆动作）。
   * 返回 Promise<boolean>：确定 true；取消 / 点遮罩 / Esc 都是 false。 */
  var confirmPending = null;

  function confirmBox(opts) {
    opts = opts || {};
    var ov = $('confirm-overlay');
    // 走查 / 纯浏览器里可能没有这个弹窗，退回原生实现
    if (!ov) return Promise.resolve(window.confirm(opts.text || ''));

    // 已经有一个开着：先把它当"取消"收掉，否则那个 Promise 永远悬着
    if (confirmPending) confirmPending(false);

    $('confirm-title').textContent = opts.title || '确认';
    /* 文案按空行切段：第一段是问句（加粗提色），后面是细节。
       用 textContent 而不是 innerHTML —— 文案里会拼进文件名，别给注入留口子。 */
    var box = $('confirm-text');
    box.textContent = '';
    String(opts.text || '').split(/\n\s*\n/).forEach(function (para) {
      if (!para) return;
      var p = document.createElement('p');
      p.textContent = para;
      box.appendChild(p);
    });
    $('confirm-hint').textContent = opts.hint || '';
    $('confirm-ok').textContent = opts.okText || '确定';
    $('confirm-cancel').textContent = opts.cancelText || '取消';
    $('confirm-ok').className = 'btn ' + (opts.danger ? 'btn-danger' : 'btn-primary');
    ov.hidden = false;
    // 焦点给确定键：键盘用户直接回车就走默认动作，Esc 走取消（见 init 的全局 Esc）
    $('confirm-ok').focus();

    return new Promise(function (resolve) {
      confirmPending = function (ok) {
        confirmPending = null;
        ov.hidden = true;
        resolve(ok);
      };
    });
  }

  // ---------------------------------------------------------------- 动作

  function pickAndOpen(which) {
    var pick = which === 'app' ? 'PickAppPack' : 'PickPatchPack';
    return call(pick)
      .then(function (path) {
        if (!path) return null;              // 用户取消
        busy(true, '正在读取…');
        return call(which === 'app' ? 'OpenAppPack' : 'OpenPatchPack', path);
      })
      .then(function (s) {
        busy(false, '');
        // 换了包：旧的选中项、上次的根路径都作废（不然会接回到别的节点上）
        sel.app = null; sel.patch = null;
        if (s) { apply(s); toast('已打开'); }
      })
      .catch(function (e) { busy(false, ''); toast(String(e && e.message || e), true); });
  }

  function reMatch() {
    busy(true, '正在匹配…');
    return call('ReMatch')
      .then(function (s) {
        busy(false, '');
        // 重新匹配可能整棵换掉（补丁包被改过），旧选中项接回去会指到别的文件
        sel.app = null; sel.patch = null;
        apply(s);
        toast('匹配完成');
      })
      .catch(function (e) { busy(false, ''); toast(String(e && e.message || e), true); });
  }

  /* collapseAll 清掉两棵树的展开态并按当前数据重建 —— 用户展开嵌套 jar 看高亮后
     想"重头来"、或者定位后想回到根视图，就用这个。不动 state 本身、不重跑匹配，
     所以后端没负担。toast 报一下清了多少条，让用户知道真的生效了。 */
  function collapseAll() {
    var n = Object.keys(expanded).length;
    clearExpanded('app');
    clearExpanded('patch');
    apply(state);
    toast('已折叠 ' + n + ' 个展开节点');
  }

  /* resetManualDel 撤销本包所有的「删除 / 标记删除」。
   *
   * 为什么需要它：标记删除还能在应用树上反向点一次，**物理删除不能** ——
   * 节点已经从树上摘掉了，右键都找不到它，可记录还在，关掉重开照样重删一遍。
   * 后端做法是"清记录 + 按原包重新解析"，所以这里得先确认：要重读一遍 jar。
   *
   * 选中态一并清掉：被删的节点回来后，它后面那些兄弟的 dom 路径会整体前移，
   * 按旧路径接回去会指到别的文件上。 */
  function resetManualDel() {
    return confirmBox({
      title: '恢复原状',
      text: '撤销本包所有的「删除 / 标记删除」？\n\n' +
            '会清掉本包保存的删除记录，并按原包重新加载应用树（大包要等几秒）。\n' +
            '已写出的新包不受影响。',
      okText: '撤销',
      danger: true
    }).then(function (ok) {
      if (!ok) return;
      sel.app = null; sel.patch = null;
      busy(true, '正在重新加载应用包…');
      return call('ResetManualDel')
        .then(function (s) {
          busy(false, '');
          if (s) apply(s);
          toast('已恢复原状');
        })
        .catch(function (e) { busy(false, ''); toast(errText(e), true); });
    });
  }

  function runPatch() {
    /* 建议名就是**应用包自己的文件名**，不带任何后缀。
       与 Java 版一致：原版 `HomeView.handleSaveAppPack` 里
       `setInitialFileName(appTree.getTreeInfo().getRootNode().getName())` ——
       根节点就是那个 jar 文件，初始名因此是 `app.jar` 而不是 `app-patched.jar`，
       初始目录也是应用包所在目录（`getAppPackSourceDir()`）。
       这里曾自行拼过 "-patched"，属于移植时多加的，已去掉。
       代价：默认值与源文件同名，直接确定会被系统提示覆盖 —— 原版就是这个行为。 */
    var suggest = state.appPath ? state.appPath.split(/[\\/]/).pop() : '';

    return call('PickSavePath', suggest)
      .then(function (out) {
        if (!out) return null;
        busy(true, '正在写出…');
        return call('RunPatch', out);
      })
      .then(function (r) {
        busy(false, '');
        if (!r) return;
        var t = r.total || {};
        // 写出后刷新一次状态：RunPatch 会清掉持久化记录，应用树上的
        // 删除标记也应随之消失（它们已经"写实"到新包里了）。
        // 不刷的话底部统计和「临时补丁」清单还挂着旧数字。
        call('GetState').then(apply).catch(function () {});
        var extra = (t.manualDel ? '\n手动删除 ' + t.manualDel : '');
        toast('已写出：' + r.output + '\n新增 ' + t.add + ' / 修改 ' + t.mod + ' / 删除 ' + t.del + extra);
      })
      .catch(function (e) { busy(false, ''); toast(String(e && e.message || e), true); });
  }

  // ---------------------------------------------------------------- 匹配连线

  /* 在两棵树之间画"补丁里的这个文件 ←→ 应用包里的那一行"的连线。

     数据从哪来：Go 侧算好放在 State.mappedLinks 里，每个元素是两端的
     **dom 路径**（`r`、`r/0/3`，即"从根往下第几个子节点"的链）。不直接给
     节点 Path 是因为嵌套 jar 里的条目 Path 不带 jar 前缀，两个 jar 里都有
     `META-INF/MANIFEST.MF` 时会连到别家去（见 gui/app.go 的 MappedLink）。
     规则必须与 Go 的 collectDomPaths 完全一致。

     为什么不用 HTML 元素做线：线要跨过中间那条分隔条、还要在树的空白处
     穿过，用 div 拼不出来；SVG 一条 path 就够，而且改坐标只要改 d。

     三处刻意的取舍：
       1. 端点接在**面板内边缘、行中心的高度**上。左树的行是 width:max-content，
          MD5 列常被面板裁掉，行内容右端根本不在可见区里，接不上。
       2. 全量画（用户选的方案 B）：当前选中那一对加粗，其余淡一档。
          补丁包的文件顺序和应用树顺序天然不同，只画一条会看不出整体。
       3. 对端滚出视口时，端点夹到可视边缘并补一个上/下小三角 ——
          否则线会突然断掉，看着像 bug。 */

  // dom 路径的起点，与 Go 侧 domRoot 保持一致
  var DOM_ROOT = 'r';

  var links = {
    list: [],
    // dom 路径 -> 行元素，两棵树各一份（两棵树的 dom 路径都从 r 开始）
    index: { app: Object.create(null), patch: Object.create(null) },
    raf: 0,
    at: 0
  };

  function linksResetIndex(treeKey) {
    links.index[treeKey] = Object.create(null);
  }

  function linksIndexRow(treeKey, dom, row) {
    var idx = links.index[treeKey];
    if (!idx) { idx = links.index[treeKey] = Object.create(null); }
    idx[dom] = row;
  }

  function linksSet(list) {
    links.list = list || [];
    linksSchedule();
  }

  /* 合并同一帧里的多次请求：展开目录、滚动、拖分隔条都会连着触发，
     每来一次就重画一遍没必要。

     注意 raf 这个"锁"不能无条件信：窗口最小化、页面被隐藏、无头渲染时
     requestAnimationFrame 的回调可能根本不执行，锁就再也回不到 0，
     之后所有重画请求都变成空操作，连线层从此冻住。
     所以加了个时间戳，超过 200ms 没等到帧就认为那一帧丢了，重新排一次。 */
  function linksSchedule() {
    var now = Date.now();
    if (links.raf && now - links.at < 200) return;
    links.at = now;
    links.raf = window.requestAnimationFrame(function () {
      links.raf = 0;
      linksDraw();
    });
  }

  /* 当前高亮哪一对：两棵树各可能有一个选中行（.row.selected），
     落在哪一对上就加粗哪一对。都没选中时所有线一个粗细。 */
  function linksFocus() {
    var a = document.querySelector('#app-tree .row.selected');
    var p = document.querySelector('#patch-tree .row.selected');
    return { app: a ? a.__dom : null, patch: p ? p.__dom : null };
  }

  /* 一行在连线层坐标系里的落点。
     返回 null 表示这一行现在不可见（父级收起了 —— 整行尺寸为 0）。
     raw 是真实中心 y；对端在视口外时 raw 会跑到画布外，最后怎么摆由 spreadEdges 定。 */
  function linkEnd(row, tree, base) {
    var r = row.getBoundingClientRect();
    if (!r.width || !r.height) return null;
    var vp = tree.getBoundingClientRect();
    var off = null;
    var edge = 0;
    if (r.bottom <= vp.top) { off = 'up'; edge = vp.top - base.top + 6; }
    else if (r.top >= vp.bottom) { off = 'down'; edge = vp.bottom - base.top - 6; }
    return { raw: r.top - base.top + r.height / 2, off: off, edge: edge, y: 0, first: false };
  }

  /* 对端在视口外时，落点只能夹到同一条边上；几条线一起夹就会叠成一个点，
     看起来像"视野外只有一个文件"，所以按真实 y 的远近沿边排成一列：
     离视口越近的越贴边，最贴边那一个画箭头（表示"这个方向还有"），其余画圆点。 */
  function spreadEdges(items, side) {
    var up = [];
    var down = [];
    for (var i = 0; i < items.length; i++) {
      var e = items[i][side];
      if (!e.off) { e.y = e.raw; continue; }
      (e.off === 'up' ? up : down).push(e);
    }
    // up：raw 越大离视口越近，越该贴边；down 反过来
    up.sort(function (p, q) { return q.raw - p.raw; });
    down.sort(function (p, q) { return p.raw - q.raw; });
    for (var k = 0; k < up.length; k++) {
      up[k].y = up[k].edge + Math.min(k, 11) * 8;   // 最多排 12 个，再多就叠着
      up[k].first = k === 0;
    }
    for (var j = 0; j < down.length; j++) {
      down[j].y = down[j].edge - Math.min(j, 11) * 8;
      down[j].first = j === 0;
    }
  }

  /* 连线路径：两个控制点都落在同一条"道"上。
     这样线离开端点时是水平的、中段贴着自己的道竖着走、到对端又水平进入 ——
     桑基图那种连线。多条线各占一条道，否则全叠在同一个 x 上会糊成一根粗线。 */
  function linkPath(x1, y1, x2, y2, laneX) {
    return 'M' + x1 + ' ' + y1 + ' C' + laneX + ' ' + y1 + ',' + laneX + ' ' + y2 + ',' + x2 + ' ' + y2;
  }

  /* 对端在视口外时的小三角（贴着可视边缘指向上/下）。 */
  function linkArrow(x, y, dir) {
    var s = 4;
    var d = dir === 'up'
      ? 'M' + x + ' ' + (y - s) + ' L' + (x - s) + ' ' + (y + s) + ' L' + (x + s) + ' ' + (y + s) + ' Z'
      : 'M' + x + ' ' + (y + s) + ' L' + (x - s) + ' ' + (y - s) + ' L' + (x + s) + ' ' + (y - s) + ' Z';
    return '<path class="off" d="' + d + '"/>';
  }

  function linksDraw() {
    var svg = $('links');
    var panes = document.querySelector('.panes');
    if (!svg || !panes) return;                 // 走查 / 纯浏览器里可能没有

    var list = links.list;
    if (!list.length) {
      if (svg.firstChild) svg.textContent = '';
      return;
    }

    var appPane = $('pane-app'), patchPane = $('pane-patch');
    var appTree = $('app-tree'), patchTree = $('patch-tree');
    if (!appPane || !patchPane || !appTree || !patchTree) return;

    var base = panes.getBoundingClientRect();
    var ra = appPane.getBoundingClientRect();
    var rp = patchPane.getBoundingClientRect();
    /* 面板被隐藏（聚焦单树）或还没布局时别画 —— 那时两边的边缘会重合。 */
    if (!ra.width || !rp.width || rp.left <= ra.right) {
      if (svg.firstChild) svg.textContent = '';
      return;
    }

    svg.setAttribute('width', base.width);
    svg.setAttribute('height', base.height);
    svg.setAttribute('viewBox', '0 0 ' + base.width + ' ' + base.height);

    // 两端都接在面板内边缘往里 2px 处
    var x1 = ra.right - base.left - 2;
    var x2 = rp.left - base.left + 2;

    var focus = linksFocus();
    var items = [];
    for (var i = 0; i < list.length; i++) {
      var l = list[i];
      var aRow = links.index.app[l.appDom];
      var pRow = links.index.patch[l.patchDom];
      if (!aRow || !pRow) continue;             // 那一行还没建出来（父级没展开）
      var a = linkEnd(aRow, appTree, base);
      var p = linkEnd(pRow, patchTree, base);
      if (!a || !p) continue;                   // 父级收起了，行不可见
      items.push({
        a: a, p: p,
        focus: focus.app === l.appDom || focus.patch === l.patchDom
      });
    }

    // 两端的落点先排边，再统一画（顺序不能反：排边会改 y）
    spreadEdges(items, 'a');
    spreadEdges(items, 'p');

    var midX = (x1 + x2) / 2;
    // 每条线一条道；条数多时道间距压窄（下限 0.9px，再窄就完全叠住了）
    var step = items.length > 1
      ? Math.max(0.9, Math.min(5, (x2 - x1 - 6) / (items.length - 1)))
      : 0;

    var out = [];
    for (var k = 0; k < items.length; k++) {
      var it = items[k];
      var laneX = items.length > 1 ? midX + (k - (items.length - 1) / 2) * step : midX;
      var cls = it.focus ? 'focus' : 'dim';
      var r = it.focus ? 3 : 2.2;

      out.push('<path class="' + cls + '" d="' + linkPath(x1, it.a.y, x2, it.p.y, laneX) + '"/>');
      // 排边那一列的顶端画箭头代替圆点，两个都画会糊成一团
      if (it.a.off && it.a.first) out.push(linkArrow(x1, it.a.y, it.a.off));
      else out.push('<circle class="' + cls + '" cx="' + x1 + '" cy="' + it.a.y + '" r="' + r + '"/>');
      if (it.p.off && it.p.first) out.push(linkArrow(x2, it.p.y, it.p.off));
      else out.push('<circle class="' + cls + '" cx="' + x2 + '" cy="' + it.p.y + '" r="' + r + '"/>');
    }
    svg.innerHTML = out.join('');
  }

  /* 连线的触发点：滚动、窗口缩放、拖分隔条。
     树的行是懒建的，展开/收起那两处已经在 makeRow 那条链上挂了 linksSchedule。 */
  function initLinks() {
    var a = $('app-tree'), p = $('patch-tree');
    if (a) a.addEventListener('scroll', linksSchedule);
    if (p) p.addEventListener('scroll', linksSchedule);
    window.addEventListener('resize', linksSchedule);
  }

  // ---------------------------------------------------------------- 拖放

  /* 把拖入窗口的文件交给后端打开。
   *
   * 为什么不用 HTML5 的 drop 事件取文件：浏览器出于安全只给 File 对象、
   * **不给真实路径**，而这个工具必须拿到路径才能建树。所以走 Wails 的原生
   * 拖放通道 —— window.runtime.OnFileDrop 回调里给的是磁盘绝对路径。
   *
   * 第二个参数 useDropTarget 传 true：只有落在挂了
   * `--wails-drop-target: drop` 的元素上才回调，也就是那两个面板
   * （见 app.css 的 .pane）与「对比」弹窗的两个文件槽（见 .cmp-slot）。
   * 落在工具栏之类的地方不触发 —— 与原版"只有树和路径框能接文件"一致。
   *
   * 注意回调里**不做收不收的判定**，只回答"落在哪一侧"（或对比弹窗的哪个槽）。
   * 收不收由 Go 侧的 HandleFileDrop / HandleCompareDrop 判（规则照搬 Java 的
   * onDragOver），这样规则留在可测的一侧，前端只管几何。 */
  function initDrop() {
    if (!window.runtime || !window.runtime.OnFileDrop) return;   // 纯浏览器里打开时没有
    window.runtime.OnFileDrop(onFileDrop, true);
    trackDropRow();
    initListDrag();
    initTreeDrag();
  }

  /* paneSideAt 判断坐标落在哪个面板里（返回 dataset.side，或 null）。
     两个面板都带 data-side（index.html 的 .pane）。 */
  function paneSideAt(x, y) {
    var el = document.elementFromPoint(x, y);
    while (el) {
      if (el.dataset && el.dataset.side) return el.dataset.side;
      el = el.parentElement;
    }
    return null;
  }

  /* 窗口内拖放：把补丁树的一行拖到应用树上（补丁树 → 应用树）。
   *
   * 为什么走 HTML5 而不是 Wails 的原生通道：原生通道只在"从操作系统拖文件
   * 进来"时触发 —— 它的 draganddrop.js 开头就判 `types.includes("Files")`，
   * 内部拖动没有 Files，直接 return。所以这里自己接，两边不会同时触发。
   *
   * 落点只认应用树：补丁侧、工具栏、窗口空白处一律静默取消（原版
   * `getTreeView() != appTree` 就 return）。真正"落点决定什么"的规则在 Go 侧
   * （DropPatchNode → service.ApplyPatchNodesToApp），前端只管几何。 */
  function initTreeDrag() {
    window.addEventListener('dragover', function (e) {
      if (!dragFromTreeNode) return;
      if (paneSideAt(e.clientX, e.clientY) !== 'app') return;
      e.preventDefault();                 // 不接就不给 drop
      if (e.dataTransfer) e.dataTransfer.dropEffect = 'copy';
    });
    window.addEventListener('drop', function (e) {
      if (!dragFromTreeNode) return;
      var src = dragFromTreeNode;
      var srcName = dragFromTreeName || src;
      dragFromTreeNode = null;
      dragFromTreeName = '';
      if (paneSideAt(e.clientX, e.clientY) !== 'app') return;   // 静默取消
      e.preventDefault();
      var el = document.elementFromPoint(e.clientX, e.clientY);
      var row = el && el.closest ? el.closest('.row') : null;
      var node = row && row.__node;
      // 落在空白处（没有行）＝ 根目录；根行也走这条路（Go 侧 isRootRow 会认）
      var rowPath = row ? locOf(row) : '';
      var rowIsDir = node ? !!node.dir : true;
      busy(true, '正在应用补丁节点…');
      call('DropPatchNode', rowPath, rowIsDir, [src])
        .then(function (s) {
          busy(false, '');
          if (!s) return;
          apply(s);
          toast('已应用：' + srcName + (rowPath ? ' → ' + rowPath : ' → 根目录'));
        })
        .catch(function (err) { busy(false, ''); toast(errText(err), true); });
    });
    window.addEventListener('dragend', function () { dragFromTreeNode = null; dragFromTreeName = ''; });
  }

  /* 从右侧「拖入的文件」条发起**窗口内**拖放（用户看到文件名就在那儿，
     想直接拖进左边某个目录，不必再回资源管理器拖一次）。
     为什么不走后端那套原生通道：原生通道只在"真·从操作系统拖文件进来"时
     才触发，这里拖的是我们自己画的一条记录；走 HTML5 的 drag 事件，把磁盘
     路径用 dragFromListPath 带过去，落点判定照样复用 onFileDrop —— 替换/新增
     的规则只有一份，不会走偏。
     全程只在 dragFromListPath 有值时才动手，所以对"真·从资源管理器拖文件"
     那条原生通道零影响（那时它是 null）。 */
  function initListDrag() {
    window.addEventListener('dragover', function (e) {
      if (!dragFromListPath) return;
      e.preventDefault();                 // 不接就不给 drop
      if (e.dataTransfer) e.dataTransfer.dropEffect = 'copy';
    });
    window.addEventListener('drop', function (e) {
      if (!dragFromListPath) return;
      e.preventDefault();                 // 拦住"把这段文本当 URL 打开"
      var p = dragFromListPath;
      dragFromListPath = null;
      /* 从右侧「拖入的文件」条拖出的文件，只接受落在：
           1. 「对比」弹窗的文件槽；或
           2. 左侧应用树（含它的空白区域，会加到根目录）。
         落在补丁面板 / 工具栏 / 其他空白处一律视为"没拖到目标"——
         静默取消，绝不再走 onFileDrop。否则 onFileDrop 在补丁侧会把
         整条列表按"未匹配"重置（showDropFile 直接覆盖 dropFiles），
         列表里两个及以上文件时，另一个会凭空消失。 */
      var slot = compareDropTarget(e.clientX, e.clientY);
      if (slot) { compareDropFiles(slot, [p]); return; }
      if (paneSideAt(e.clientX, e.clientY) === 'app') onFileDrop(e.clientX, e.clientY, [p]);
      // 否则：拖回原处 / 落在补丁侧 / 落在中性区域 —— 静默取消，列表不动
    });
    window.addEventListener('dragend', function () {
      dragFromListPath = null;
    });
  }

  /* 拖动经过时只高亮"光标下最深的那一行"。
   *
   * 为什么不让 Wails 那套 wails-drop-target-active 来干：它会把 class 加到
   * 落点**及其所有带该属性的祖先**上，于是光标停在一个深层文件上时，整列
   * 目录行都会亮 —— 看不出到底会作用在哪一行。这里自己取 elementFromPoint
   * 的最近 .row，只给它挂 .drop-row，语义就清楚了：
   *   - 文件行亮 = 松手会**替换**这个文件；
   *   - 目录行亮 = 松手会把文件**加进**这个目录。
   * 拖 .jar / .zip 也照样亮：在应用树的行上它被当成"包内文件"处理
   * （见 Go 的 HandleDrop），所以这个提示对整包同样准确 —— 早期不准确，
   * 那时行会亮但松手是"换掉整个应用包"。
   * 面板级的虚线框仍由 Wails 负责（.pane.wails-drop-target-active），两者不冲突。 */
  var dropRow = null;
  function trackDropRow() {
    function isFileDrag(e) {
      var t = e.dataTransfer && e.dataTransfer.types;
      /* 三种都算"在拖文件"：从资源管理器拖进来的**真** OS 文件（types 里有
         Files）、从右侧「拖入的文件」条拖向应用树的那一条（拖动期间
         dragFromListPath 有值）、以及从补丁树拖出的一行（dragFromTreeNode）。
         后两者 dataTransfer 里只有我们塞的东西、没有 Files，不补这两条的话
         目标行不会亮，用户松手前完全看不到会落在哪。 */
      if (dragFromListPath || dragFromTreeNode) return true;
      return !!t && Array.prototype.indexOf.call(t, 'Files') >= 0;
    }
    function sync(e) {
      if (!isFileDrag(e)) return;
      var el = document.elementFromPoint(e.clientX, e.clientY);
      var row = el && el.closest ? el.closest('.row') : null;
      if (row === dropRow) return;
      if (dropRow) dropRow.classList.remove('drop-row');
      dropRow = row;
      if (row) row.classList.add('drop-row');
    }
    function clear() {
      if (dropRow) { dropRow.classList.remove('drop-row'); dropRow = null; }
    }
    window.addEventListener('dragover', sync, true);
    window.addEventListener('drop', clear, true);
    window.addEventListener('dragend', clear, true);
    window.addEventListener('dragleave', function (e) {
      if (!e.relatedTarget) clear();      // 拖出窗口
    }, true);
  }

  /* 一次拖放的最终落点判定 + 分流。
   *
   * 落点分三种：
   *   1. 「对比」弹窗的文件槽 —— 填槽，不碰两棵树；
   *   2. 某一棵树的**具体文件行**（不是目录、不是空白处）—— 走"替换节点内容"：
   *      把拖进来的普通文件换成这个节点在包里的内容（单文件替换）；
   *   3. 否则按面板归属（data-side）走"打开包"语义。
   *
   * 行判定用 document.elementFromPoint 取光标下最深的 .row，再看它挂的节点
   * 是不是文件（目录行不算，落上去按空白处理）。「是不是文件行」连同 rowPath
   * 一起送进 Go 的 HandleDrop，由它决定替换还是打开 —— 规则留在后端可单测。
   *
   * 注意：Wails 的 useDropTarget=true 已经保证回调只在落在带
   * `--wails-drop-target` 的元素上才触发（.pane 及其子元素都继承了这个属性，
   * 含 .row），所以这里不需要再判"落点是不是合法区域"。 */
  function onFileDrop(x, y, paths) {
    if (working) return;                       // 对应原版 isLoadingFlag()
    /* 落在「对比」弹窗的文件槽上：填槽，不走两棵树那套 */
    var slot = compareDropTarget(x, y);
    if (slot) { compareDropFiles(slot, paths); return; }

    var el = document.elementFromPoint(x, y);
    var row = el && el.closest ? el.closest('.row') : null;
    var node = row && row.__node;
    var rowPath = node ? node.path : '';
    var rowIsDir = !!(node && node.dir);
    var side = rowPath ? row.__tree : paneAt(x, y);
    if (!side) return;

    /* 落点语义：
         - 应用侧 文件行 + 单个文件 → 替换这个节点；
         - 应用侧 目录行 + 文件（拖几个都行）→ 按名字加进该目录；
         - 应用侧 空白处 + 普通文件 → 加到根目录；
         - **应用侧的行上，拖 .jar / .zip 也走上面三条** —— 整包在行上被当成
           "包内文件"（原版 FileTreeCell 的分工），只有落在空白处才是"打开整包"。
           早期这里"整包与落点无关"，于是拖个 jar 到 BOOT-INF/lib 上会把整个
           应用包换掉，用户当场报了"不支持其他 jar 包直接拖入，应该和文件一样"；
         - 补丁侧：普通文件按名字反查应用树，整包 / 目录一律"打开补丁包"
           （补丁树的单元格原版就不接拖放）。
       具体走哪条全在 Go 的 HandleDrop 里判，这里只把几何（side / rowPath /
       rowIsDir）送过去。 */
    var name = baseName(paths[0]);
    /* 右侧面板顶部先把"拖进来的是什么"显示出来，别等后端返回 ——
       反馈要贴着落点，慢半拍也算慢。只记补丁侧：应用侧拖入是就地替换/新增，
       结果本来就在树上高亮了。整包/目录拖进来算「打开」，头部已经显示包路径，
       不需要这条记录。 */
    var isPackDrop = paths.length === 1 && /\.(jar|zip|war|ear)$/i.test(paths[0]);
    if (side === 'patch' && !isPackDrop) showDropFile(paths);
    busy(true, '正在处理…');
    call('HandleDrop', side, rowPath, rowIsDir, paths)
      .then(function (r) {
        busy(false, '');
        if (!r) return;
        if (r.items && r.items.length) setDropFileMeta(r.items);
        else if (r.srcSize > 0 || r.srcMd5) {
          setDropFileMeta([{ srcPath: r.srcPath, srcSize: r.srcSize, srcMd5: r.srcMd5 }]);
        }
        if (r.action === 'open') clearDropFile();
        /* 补丁侧拖入普通文件 → 按文件名在应用树里反查。命中结果落到下方
           「补丁信息」面板里，由用户点行定位、看过再替换（不在这里自动替换）。 */
        if (r.action === 'matchMany' || r.action === 'matchNone') {
          showPending(r);
          var hits = 0;
          (r.items || []).forEach(function (it) { hits += (it.candidates || []).length; });
          toast(hits
            ? '匹配到 ' + hits + ' 处，见下方「补丁信息」'
            : '没有匹配到同名文件，见下方「补丁信息」');
          return;
        }
        if (!r.state) return;
        apply(r.state);
        if (r.action === 'same') {
          /* 同名 + md5 相同：后端**什么都没改**（连「已修改」标记都没打）。
             这里不能报"已替换"—— 用户会以为内容被换过了，其实字节一个没变。
             标成 same 而不是 replaced，右侧那条记录也就不会挂着假状态。 */
          markDropFile('same', r.srcPath, r.path);
          toast('「' + name + '」与 ' + r.path + ' 内容完全相同，未做改动');
          revealByPath(side, r.path);
        } else if (r.action === 'replace') {
          markDropFile('replaced', r.srcPath, r.path);
          toast('已用「' + name + '」替换 ' + r.path);
          revealByPath(side, r.path);
        } else if (r.action === 'add') {
          /* 新增可能一次拖多个：每条拖入文件都标成"已加入"，
             别只标第一个，否则面板里其余几条还停在「未定位」让人误会。 */
          paths.forEach(function (p) { markDropFile('added', p, r.path); });
          toast(r.count > 1 ? '已加入 ' + r.count + ' 个文件' : '已加入 ' + r.path);
          revealByPath(side, r.path);
        } else {
          toast('已打开');
        }
      })
      .catch(function (e) { busy(false, ''); toast(String(e && e.message || e), true); });
  }

  function baseName(p) {
    return String(p || '').split(/[\\/]/).pop();
  }

  /* ------------------------------------------- 拖入的文件（右侧「补丁包」面板）

     把文件拖到右侧时，在面板顶部逐条列出"手上拿的是什么"：文件名 + 体积 +
     所在目录。之前拖完右侧一点反馈都没有（补丁包还没打开时尤其明显），
     只能盯底部状态栏猜到底拖没拖进去、拖的是哪个文件 —— 反馈离落点太远。

     一次拖多个就列多行。**不折叠成"xxx 等 2 个"**：名字是唯一能确认
     "我拖对了没"的信息，折叠成一个数字等于没说。

     这条只**记录**，不参与任何动作：真正的处理还是 HandleDrop 那一套
     （替换 / 新增 / 按名反查）。所以「清除」只是把记录抹掉，不改动任何内容。

     state 记这条"拖入文件"到底落到哪了 —— 用户最容易误会的就是：拖进来、粘了
     个路径，以为它已经躺在那个目录下、会跟着打补丁一起出去。其实粘来的是**目录**
     时根本不能替换，这条就一直停在「未定位」，打补丁也不会把它带出去。把这层
     状态露出来，比任何文字解释都挡得住误会。 */
  var dropFiles = [];      // [{ path, size, md5, state, target }]  state: pending|replaced|added|same

  /* 正被"从这条记录往树上拖"的源文件磁盘路径；不在拖的时候为 null。
     不用 dataTransfer 带：dragover 阶段读不到 getData，只有 drop 才拿得到，
     而"拖动中要高亮目标行"恰恰要在 dragover 就知道拖的是什么，所以用模块变量。 */
  var dragFromListPath = null;
  /* 从补丁树某一行拖出时记下它的**定位串**（含 `jar!/` 前缀，见 locOf）。
     只在内部拖动期间有值；与 dragFromListPath 互斥地使用 ——
     两者都为 null 时，拖放完全交给 Wails 的原生通道。 */
  var dragFromTreeNode = null;
  /* 同时记一下那一行的显示名：根行的定位串是**磁盘路径**，直接甩进提示里
     又长又难认，说"已应用：xxl-job-core-3.6.0.jar"就够了。 */
  var dragFromTreeName = '';

  function showDropFile(paths) {
    dropFiles = (paths || []).map(function (p) {
      return { path: String(p || ''), size: 0, md5: '', state: 'pending', target: '' };
    });
    renderDropFile();
  }

  /* markDropFile 把某条拖入文件标成"已生效"。
     按 srcPath 找 —— 一份内容只会落一遍，多条拖入互不干扰。 */
  function markDropFile(state, srcPath, target) {
    var hit = false;
    for (var i = 0; i < dropFiles.length; i++) {
      if (dropFiles[i].path === srcPath) {
        dropFiles[i].state = state;
        dropFiles[i].target = target || '';
        hit = true;
      }
    }
    if (hit) renderDropFile();
  }

  /* setDropFileMeta 用后端回来的体积 / md5 补上。按路径对齐 —— 后端 Items 的
     顺序与拖入顺序一致，但按路径匹配更稳（将来乱序也不会错位）。

     两个值都是"拿不到就留空"：srcSize 为 0 时不显示体积、srcMd5 为空串时
     不显示 MD5，绝不拿 0 或假值糊上去。 */
  function setDropFileMeta(items) {
    if (!items || !items.length || !dropFiles.length) return;
    var byPath = {};
    items.forEach(function (it) {
      if (it && it.srcPath) byPath[it.srcPath] = { size: it.srcSize || 0, md5: it.srcMd5 || '' };
    });
    var hit = false;
    dropFiles.forEach(function (f) {
      var m = byPath[f.path];
      if (!m) return;
      if (m.size > 0) { f.size = m.size; hit = true; }
      if (m.md5) { f.md5 = m.md5; hit = true; }
    });
    if (hit) renderDropFile();
  }

  function renderDropFile() {
    var bar = $('drop-file');
    var list = $('drop-file-list');
    if (!bar || !list) return;
    list.textContent = '';
    /* 只有 1 个文件时不显示计数 —— 空药丸会变成一根突兀的小横线。 */
    var cnt = $('drop-file-count');
    cnt.textContent = dropFiles.length > 1 ? dropFiles.length + ' 个' : '';
    cnt.hidden = dropFiles.length <= 1;

    var MAX = 8;      // 再多就把面板挤没了；超出的折成一行说明
    dropFiles.slice(0, MAX).forEach(function (f) {
      var li = document.createElement('li');
      li.className = 'dropfile-item';
      li.title = f.path + '\n可拖到左侧应用树：落到哪一行只决定「加进哪个目录」，'
        + '再按文件名找同名项 —— 有就覆盖、没有就新增';
      /* 这条记录本身可当拖拽源：用户看到文件名摆在这儿，就想直接拖进左边
         某个目录里，没必要再回资源管理器拖一次（真实用户就是这么试的）。
         落点判定复用 onFileDrop，见 initListDrag。 */
      li.draggable = true;
      li.addEventListener('dragstart', function (e) {
        dragFromListPath = f.path;
        if (e.dataTransfer) {
          e.dataTransfer.effectAllowed = 'copy';
          try { e.dataTransfer.setData('text/plain', f.path); } catch (err) {}
        }
      });

      var n = document.createElement('span');
      n.className = 'dropfile-name';
      n.textContent = baseName(f.path);
      li.appendChild(n);

      if (f.size > 0) {
        var s = document.createElement('span');
        s.className = 'dropfile-size';
        s.textContent = humanSize(f.size);
        li.appendChild(s);
      }

      /* MD5：与左边树行同一列信息（原版 FileTreeCell 是
         `日期 大小 md5` 一锅端）。拖完后拿它和左边目标文件那一行的 MD5
         对一眼，就能确认"换进去的是不是同一份内容"—— 比文件名可靠得多。
         后端还没算出来时（f.md5 为空）整个 span 不建，不留占位空档。 */
      if (f.md5) {
        var m = document.createElement('span');
        m.className = 'dropfile-md5';
        m.textContent = f.md5;
        m.title = 'MD5：' + f.md5;
        li.appendChild(m);
      }

      var p = document.createElement('span');
      p.className = 'dropfile-path';
      p.textContent = dirName(f.path);
      li.appendChild(p);

      /* 状态药丸：pending（未定位）/ replaced（已替换）/ added（已加入）/
         same（内容相同，没改动）。
         这是"这条拖入到底有没有生效"的唯一肉眼可见信号 —— 没它，用户只能从
         左下角那三个数猜。same 单列一档而不是复用 replaced：内容一样时
         什么都不该变，报"已替换"等于骗人。 */
      var st = document.createElement('span');
      st.className = 'dropfile-state dropfile-state-' + (f.state || 'pending');
      st.textContent = f.state === 'replaced' ? '已替换'
        : f.state === 'added' ? '已加入'
        : f.state === 'same' ? '内容相同'
        : '未定位';
      if (f.state === 'same') {
        st.title = '这份内容与包里的完全相同，没有做任何改动';
      }
      li.appendChild(st);

      list.appendChild(li);
    });
    if (dropFiles.length > MAX) {
      var more = document.createElement('li');
      more.className = 'dropfile-more';
      more.textContent = '还有 ' + (dropFiles.length - MAX) + ' 个文件';
      list.appendChild(more);
    }

    bar.hidden = dropFiles.length === 0;
    linksSchedule();      // 面板高度变了，树区跟着变，连线要重算
  }

  /* dirName 取磁盘路径里的目录部分（只用于展示，不参与任何逻辑）。 */
  function dirName(p) {
    var s = String(p || '');
    var i = Math.max(s.lastIndexOf('/'), s.lastIndexOf('\\'));
    return i > 0 ? s.slice(0, i) : s;
  }

  function clearDropFile() {
    var bar = $('drop-file');
    if (!bar || bar.hidden) return;
    dropFiles = [];
    bar.hidden = true;
    linksSchedule();
  }

  /* ------------------------------------------------- 拖入待替换（补丁信息面板）

     场景：手里有一个 Foo.class，但忘了它在 jar 的哪个目录。把它拖到**补丁侧**，
     后端按文件名反查出所有同名节点，命中结果列在「补丁信息」面板顶部。

     两步走（用户提的："点击后跳转，查看没问题才进行替换"）：
       1. 点清单里的一行 → 在应用树里定位、展开、选中它；
       2. 看过没问题，点右上角「替换到选中项」→ 这时才真正改内容。

     为什么不用弹窗：弹窗正好挡住树，而这一步的关键恰恰是**看树**（路径对不对、
     旁边是不是那个类）；而且弹窗里点一行就替换，等于没看就动手 —— 同名但不同包
     的类太常见了，反查错了没有任何拦截。

     状态只有一份 pending：新的一次拖入顶掉旧的（旧的没替换过，丢掉无损失）。

     **一次可以拖多个文件**：后端按文件分组返回（items），每组自带 srcPath。
     所以选中态记的是 { path, srcPath, fileName } 三件套 —— 只记 path 的话，
     多文件时替换会用错文件的内容（把 A 的内容写进 B 的位置）。 */
  var pending = null;      // { items: [{fileName, srcPath, srcSize, candidates, placeholders, scanNote}], sel }

  function clearPending() {
    pending = null;
    var box = $('pending');
    if (box) box.hidden = true;
    var list = $('pending-list');
    if (list) list.textContent = '';
    var ok = $('pending-ok');
    if (ok) ok.disabled = true;
    var inp = $('pending-path-input');
    if (inp) inp.value = '';
    setPathNote('');
    var badge = $('info-badge');
    if (badge) { badge.hidden = true; badge.textContent = ''; }
    document.body.classList.remove('has-pending');
    document.body.classList.remove('pending-tall');
    linksSchedule();      // 面板高度变了，树区跟着变，连线要重算
  }

  /* pendingItems 把 DropResult 归一成"每个文件一条"的数组。
     后端 matchMany / matchNone 都带 items；万一拿到旧格式（扁平字段），
     也包成一条，不至于整块渲染不出来。 */
  function pendingItems(r) {
    var raw = r && r.items;
    if (raw && raw.length) {
      return raw.map(function (it) {
        return {
          fileName: it.fileName || '',
          srcPath: it.srcPath || '',
          srcSize: it.srcSize || 0,
          candidates: it.candidates || [],
          placeholders: it.placeholders || [],
          scanNote: it.scanNote || ''
        };
      });
    }
    return [{
      fileName: (r && r.fileName) || '',
      srcPath: (r && r.srcPath) || '',
      srcSize: (r && r.srcSize) || 0,
      candidates: (r && r.candidates) || [],
      placeholders: (r && r.placeholders) || [],
      scanNote: (r && r.scanNote) || ''
    }];
  }

  /* showPending 渲染一次拖入的结果。
     r 是 Go 侧 DropResult：matchMany（至少一个文件有候选）或 matchNone。
     没找到也走这里 —— 否则用户盯着面板看是空的，不知道到底发生了什么。 */
  function showPending(r) {
    clearPending();
    var box = $('pending');
    var list = $('pending-list');
    if (!box || !list) return;

    var items = pendingItems(r);
    pending = { items: items, sel: null };

    var total = 0, phTotal = 0, missCount = 0;
    items.forEach(function (it) {
      total += it.candidates.length;
      phTotal += it.placeholders.length;
      if (!it.candidates.length && !it.placeholders.length) missCount++;
    });
    var multi = items.length > 1;
    var one = items[0];
    var srcSize = one.srcSize > 0 ? '拖入 ' + humanSize(one.srcSize) + ' · ' : '';

    /* 标题：单文件时点名是哪个文件；多文件时只报总数 ——
       把 5 个名字挤进一行标题，反而一个都看不清（每组的名字在分组行里）。 */
    $('pending-title').textContent = multi
      ? '拖入 ' + items.length + ' 个文件，共匹配到 ' + total + ' 处'
      : (total
        ? '「' + one.fileName + '」匹配到 ' + total + ' 处'
        : (one.placeholders.length ? '「' + one.fileName + '」不在应用包里' : '「' + one.fileName + '」没有匹配'));

    /* 提示把话说清。几种情况分开讲，因为用户的下一步动作完全不同：
       1. 有候选 → 点一行定位、确认无误再替换；
       2. 只有占位 → 树上有、包里没有（补丁造的目标位），最常见误会，最需要解释；
       3. 真没有 → 报出"查了多少"，让人能判断是工具没查到还是包确实没有。 */
    if (multi) {
      $('pending-hint').textContent = total
        ? '点一行在应用树里定位查看，确认无误再替换；每行用的是它自己那个文件'
        : '这 ' + items.length + ' 个文件都没找到同名项 —— 在下面粘贴包内路径定位，或把文件拖到具体文件行上';
    } else if (total) {
      $('pending-hint').textContent = srcSize + '点一行在应用树里定位查看，确认无误再替换';
    } else if (one.placeholders.length) {
      $('pending-hint').textContent =
        '这些位置是补丁按 README 新建的，包里原本没有这个文件 —— ' +
        '要换成你这份，请把文件拖到补丁树里同名的那一行';
    } else {
      $('pending-hint').textContent = (one.scanNote ? one.scanNote + '，' : '') +
        '没有同名文件 —— 把文件拖到某个文件行上替换，或在下面粘贴包内路径定位';
    }
    $('pending-hint').title = one.srcPath || '';

    /* 列表：多文件时按文件分组，每组一个细标题（文件名 + 命中数），
       命中为 0 的组也要出现 —— "这个没找到"本身就是用户最需要的信息。
       单文件时不画分组行（标题里已经点名了），省一行高度。 */
    items.forEach(function (it) {
      if (multi) list.appendChild(pendingGroup(it));
      it.candidates.forEach(function (c) { list.appendChild(pendingRow(c, false, it)); });
      it.placeholders.forEach(function (c) { list.appendChild(pendingRow(c, true, it)); });
    });

    $('pending-ok').disabled = true;    // 没选中就不能替换：先看，再动手
    box.hidden = false;
    /* 面板标题上挂个角标：清单在面板下部，光靠这块浅蓝底还不够显眼 ——
       "拖完不知道下头有东西要看"是真实反馈。 */
    var badge = $('info-badge');
    if (badge) {
      badge.textContent = total ? '待替换 ' + total + ' 处' : '没有匹配';
      badge.hidden = false;
      badge.classList.toggle('is-miss', total === 0);
    }
    document.body.classList.add('has-pending');
    // 有候选才把面板抬高；只有占位说明时一行就够，别白占树的地方
    document.body.classList.toggle('pending-tall', total > 0 || phTotal > 1);
    linksSchedule();

    /* 只有一处命中时**直接定位过去**：这种情况没什么可挑的，让人再点一下
       纯属多余，而"拖进来之后树上什么都没发生"看起来就像没成功
       （真实反馈："提示这样的，并没有展开"）。
       仍然会选中它（「替换到选中项」随之可用），但替换永远要再点一次。 */
    if (!multi && total === 1 && !phTotal) {
      pendingSelect(items[0], items[0].candidates[0].path);
      $('pending-hint').textContent = srcSize + '已在应用树里定位到唯一一处，确认无误后点「替换到选中项」';
    }
  }

  /* pendingGroup 画一条"文件分组"标题：文件名 + 命中数 + 体积。
     多文件拖入时用来把候选按来源文件分开，避免"这条该用哪份内容替换"搞混。 */
  function pendingGroup(it) {
    var li = document.createElement('li');
    li.className = 'pending-group';
    li.title = it.srcPath || '';

    var name = document.createElement('span');
    name.className = 'pending-group-name';
    name.textContent = it.fileName;
    li.appendChild(name);

    var note = document.createElement('span');
    note.className = 'pending-group-note';
    var n = it.candidates.length;
    if (n) {
      note.textContent = '匹配到 ' + n + ' 处';
    } else if (it.placeholders.length) {
      note.textContent = '包里没有（补丁新增的位置）';
    } else {
      note.textContent = '没有匹配';
      li.classList.add('is-miss');
    }
    li.appendChild(note);

    if (it.srcSize > 0) {
      var sz = document.createElement('span');
      sz.className = 'pending-group-size';
      sz.textContent = humanSize(it.srcSize);
      li.appendChild(sz);
    }
    return li;
  }

  /* pendingRow 渲染一条候选。两行：条目路径（可换行、不截断）+「在 xxx.jar」。
     嵌套 jar 里同名文件靠 jar 名区分，单行省略恰好会把唯一的区别截掉。

     item 是这条候选所属的拖入文件（多文件时决定替换用哪份内容）。
     placeholder=true 表示这条**不在包里**（补丁按 README 造出来的目标位）：
     点它只定位、不能选中替换，并打一个「补丁新增」的标签说明身份。 */
  function pendingRow(c, placeholder, item) {
    var loc = String(c.path || '');
    var cut = loc.indexOf('!/');
    var inner = cut >= 0 ? loc.slice(cut + 2) : loc;
    var jar = cut >= 0 ? loc.slice(0, cut) : '';

    var li = document.createElement('li');
    var btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'pending-item' + (placeholder ? ' is-placeholder' : '');
    btn.title = loc;                     // 完整定位串，悬停可见

    var main = document.createElement('span');
    main.className = 'pending-main';
    var p = document.createElement('span');
    p.className = 'pending-path';
    p.textContent = inner;
    main.appendChild(p);
    if (jar) {
      var j = document.createElement('span');
      j.className = 'pending-jar';
      j.textContent = '在 ' + jar;
      main.appendChild(j);
    }
    btn.appendChild(main);

    if (typeof c.size === 'number' && c.size >= 0) {
      var sz = document.createElement('span');
      sz.className = 'pending-size';
      sz.textContent = humanSize(c.size);
      btn.appendChild(sz);
    }
    if (placeholder) {
      var tag = document.createElement('span');
      tag.className = 'pending-tag';
      tag.textContent = '补丁新增';
      btn.appendChild(tag);
    } else if (c.md5 && item && item.srcMd5 && c.md5 === item.srcMd5) {
      /* 内容与拖入的那份完全相同 → 点之前就标出来。
         这时"替换"是空操作，用户看一眼就不用点了；真点了也会被
         pendingApply 拦下来（那边有同一套判断，防止清单没刷新时误替换）。
         两侧 md5 都是后端一起下发的，这里不比不额外问。 */
      var sameTag = document.createElement('span');
      sameTag.className = 'pending-tag is-same';
      sameTag.textContent = '内容相同';
      sameTag.title = '这一处的内容与你拖入的文件完全相同，替换不会有任何变化';
      btn.appendChild(sameTag);
    }

    /* 点一行只**定位**，不动内容 —— 让"看看再说"永远是免费的。
       占位节点只定位：它不在包里，替换它没有意义（要改用你这份，
       得去补丁树里替换那一份）。 */
    btn.addEventListener('click', function () {
      if (placeholder) { revealByPath('app', c.path); return; }
      pendingSelect(item, c.path);
    });
    li.appendChild(btn);
    return li;
  }

  /* pendingSelect 选中一条候选：清单里高亮，同时在应用树里定位并选中它。
     item 一并记下来 —— 多文件拖入时，替换必须用**这条所属的那个文件**。 */
  function pendingSelect(item, path) {
    if (!pending) return;
    pending.sel = {
      path: path,
      srcPath: item ? item.srcPath : '',
      fileName: item ? item.fileName : '',
      /* 源文件的 md5 带着走：按路径指定目标时 item 可能为空，那就退到
         "清单里唯一那个文件"—— 见 pendingTargetItem 的取舍。 */
      srcMd5: item ? (item.srcMd5 || '') : ''
    };
    var rows = $('pending-list').querySelectorAll('.pending-item');
    for (var i = 0; i < rows.length; i++) {
      rows[i].classList.toggle('active', rows[i].title === path);
    }
    /* 没有源文件路径时不能替换 —— 手上没有新内容可写，按下去只会写坏。
       正常情况下 matchMany / matchNone 每个 item 都带 srcPath。 */
    $('pending-ok').disabled = !pending.sel.srcPath;
    /* 角标跟着选中态走：从清单里点的那一处，保持"待替换 N 处"；
       用路径**指定**的那一处不在清单里，改成"已指定目标" —— 否则角标还挂着
       "没有匹配"，而下面已经定好要替换哪儿了，自相矛盾。 */
    var listed = false;
    if (item && item.candidates) {
      for (var k = 0; k < item.candidates.length; k++) {
        if (item.candidates[k].path === path) { listed = true; break; }
      }
    }
    if (!listed) setBadge('已指定目标', false);
    /* 定位失败要**说出来**：这一步的全部意义就是"让用户看一眼"，
       没滚过去等于没看，那就不能让他以为看过了。选中态保留 ——
       路径在清单里写着，他还能自己对。 */
    revealByPath('app', path).then(function (ok) {
      if (!ok) toast('没能定位到该项，请按清单里的路径自行核对后再替换', true);
    });
  }

  /* pendingCandidateOf 在待替换清单里按路径找那一项（返回候选本身）。
     只用来比 md5 —— 候选的 md5 由后端一起下发（`MatchCandidate.Md5`）。 */
  function pendingCandidateOf(path) {
    if (!pending || !path) return null;
    for (var i = 0; i < pending.items.length; i++) {
      var cs = pending.items[i].candidates || [];
      for (var k = 0; k < cs.length; k++) {
        if (cs[k].path === path) return cs[k];
      }
    }
    return null;
  }

  /* pendingApply 真正替换。走到这里说明用户已经在树上确认过那一项了。 */
  function pendingApply() {
    if (!pending || !pending.sel) return;
    var sel = pending.sel;
    if (!sel.srcPath) {
      /* 理论上走不到（每个 item 都带 srcPath），但真到了这里
         千万别带着空路径去调替换 —— 后端要么报错要么写坏文件。 */
      toast('这次拖入没拿到源文件路径，请把文件重新拖一次', true);
      return;
    }
    /* 内容完全相同就别替换了。替换成同一份内容在语义上是空操作，但会给节点
       打上「已修改」星号、清掉它的 md5 缓存 —— 用户以为改了，字节一个没变，
       保存出来也看不出差别。两侧 md5 都是现成的（候选行带 md5，拖入那条带
       srcMd5），比一下就行，不必再问后端。
       拿不到任一侧就照常替换：宁可多换一次，也不能因为"算不出来"把文件丢掉。 */
    var cand = pendingCandidateOf(sel.path);
    if (cand && cand.md5 && sel.srcMd5 && cand.md5 === sel.srcMd5) {
      clearPending();
      markDropFile('same', sel.srcPath, sel.path);
      toast('「' + sel.fileName + '」与 ' + sel.path + ' 内容完全相同，未做改动');
      revealByPath('app', sel.path);
      return;
    }
    busy(true, '正在替换…');
    call('ReplaceNodeContent', 'app', sel.path, sel.srcPath)
      .then(function (s) {
        busy(false, '');
        if (!s) return;
        apply(s);
        clearPending();
        /* 这条拖入文件既然已经替换成功，把状态从「未定位」翻成「已替换」——
           否则面板里它一直挂着"未定位"，用户会以为它没生效、甚至以为会漏出去。 */
        markDropFile('replaced', sel.srcPath, sel.path);
        toast('已用「' + sel.fileName + '」替换 ' + sel.path);
        revealByPath('app', sel.path);    // 替换完仍然把它留在视野里
      })
      .catch(function (e) { busy(false, ''); toast(errText(e), true); });
  }

  /* pendingTargetItem 决定"按路径定位"要用哪份内容去替换。
     依次看：已选中的那一行所属的文件 → 只有一个文件时就是它 →
     只有一个文件没匹配到（按路径显然是为它）→ 否则问不出来，让用户先点一行。 */
  function pendingTargetItem() {
    if (!pending) return null;
    var i;
    if (pending.sel) {
      for (i = 0; i < pending.items.length; i++) {
        if (pending.items[i].srcPath === pending.sel.srcPath) return pending.items[i];
      }
    }
    if (pending.items.length === 1) return pending.items[0];
    var miss = pending.items.filter(function (it) {
      return !it.candidates.length && !it.placeholders.length;
    });
    if (miss.length === 1) return miss[0];
    return null;
  }

  /* setPathNote 在路径输入框下面写一行解释（空串 = 收起）。
     内容是我们自己的文案，只把路径/文件名这类外部字符串 escape 后插进去。
     为什么不用 toast 代替：toast 几秒就没，而"这是目录，请粘到文件那一层"
     是用户要照着做的下一步，得留在眼前。 */
  function setPathNote(html, warn) {
    var el = $('pending-note');
    if (!el) return;
    if (!html) {
      el.hidden = true;
      el.textContent = '';
      el.classList.remove('is-warn');
      document.body.classList.remove('has-pathnote');
      linksSchedule();
      return;
    }
    el.innerHTML = html;
    el.classList.toggle('is-warn', !!warn);
    el.hidden = false;
    /* 面板默认只有 150px 高，解释行在输入框下面、会被直接切掉（踩过：
       截图里只看到输入框，"为什么不能替换"一个字都没露出来）。
       有解释行时和"候选多"一样把面板抬起来。 */
    document.body.classList.add('has-pathnote');
    linksSchedule();      // 面板高度变了，树区跟着变，连线要重算
  }

  /* setBadge 更新「补丁信息」标题右侧的角标。 */
  function setBadge(text, isMiss) {
    var badge = $('info-badge');
    if (!badge) return;
    badge.textContent = text || '';
    badge.hidden = !text;
    badge.classList.toggle('is-miss', !!isMiss);
  }

  /* pendingPathGo 按「包内路径」匹配：名字反查不到时（文件本来就不在包里、
     或者同名太多分不清）直接把路径粘进来定位。

     用户手上那串路径形态很杂 —— 从解压工具复制的带反斜杠、从报错栈复制的带
     jar 前缀（BOOT-INF/lib/x.jar!/com/foo/Bar.class）、前面还挂着
     D:/work/app.jar 甚至包着引号。清洗与解析一律放 Go 侧
     （normalizeInsidePath / ResolveAppPath），前端只管原样传过去，避免两套规则
     各修各的、最后对不上。 */
  function pendingPathGo() {
    var inp = $('pending-path-input');
    if (!inp) return;
    var v = String(inp.value || '').trim();
    if (!v) { toast('先粘一个包内路径', true); inp.focus(); return; }
    /* 定位到之后要用**哪份内容**去替换？多文件拖入时必须问清楚，
       否则会把 A 的内容写进 B 的位置。判定规则见 pendingTargetItem。 */
    var item = pendingTargetItem();
    if (!item) {
      toast('有多个拖入文件 —— 先在清单里点一行，确定这一处要用哪个文件替换', true);
      return;
    }
    setPathNote('');
    busy(true, '正在按路径查找…');
    call('ResolveAppPath', v)
      .then(function (r) {
        busy(false, '');
        if (!r) return;
        if (r.missing) {
          /* 包里真没有。把清洗后的路径回显出来 —— 用户多半是路径抄错了一截，
             对着回显改比对着原文猜快。 */
          inp.value = r.path || v;
          setPathNote('应用包里没有 <code>' + escapeHtml(r.path || v) + '</code>。' +
            '先核对路径有没有多一截或少一截（大小写、中间的 <code>!/</code> 都要对）；' +
            '确实没有的话，直接把文件拖到应用树里目标那一行即可新增。', true);
          toast('应用包里没有这个路径', true);
          return;
        }
        if (r.dir) { pendingPathDir(r, item, inp); return; }
        if (r.placeholder) {
          /* 树上有这一行，但那是补丁按 README 造出来的目标位，包里原本没有。
             可以定位（让人看清是哪儿），不能替换 —— 替换它等于凭空往应用包里
             加一个文件，不是这次想干的事。 */
          inp.value = r.path;
          revealByPath('app', r.path);
          setPathNote('<code>' + escapeHtml(r.path) + '</code> 是补丁按 README 新建的位置，' +
            '应用包里原本没有这个文件 —— 所以不能拿它当替换目标。', true);
          toast('这是补丁新增的位置，包里没有，已为你定位', true);
          return;
        }
        inp.value = r.path;             // 回填清洗后的路径，方便和原文对照
        pendingSelect(item, r.path);    // 高亮 + 定位，看过再点「替换到选中项」
        setPathNote('已定位到 <code>' + escapeHtml(r.path) + '</code>，' +
          '在树上确认无误后点「替换到选中项」。');
        toast('已定位到 ' + r.path);
      })
      .catch(function (e) { busy(false, ''); toast(errText(e), true); });
  }

  /* pendingPathDir 粘进来的是**目录**。

     最常见的来源就是右键「复制路径」：它拼的是节点名字（nodeDisplayPath），
     目录名天然不带尾斜杠，所以粘回来"看着就是树上那一行"。可那一行下面是一堆
     文件，拿它当替换目标没有意义 —— 早先直接回"应用包里没有这个路径"，
     等于指着一个明明就在眼前的目录说它不存在。

     先按最可能的意思试一次：**目录 + 手上这个文件名**。用户拖进来 expdp.txt、
     又粘了 factory 的路径，多半就是"这个文件在 factory 里"。拼出来真有一个
     文件就选中它，并在提示里明说是拼出来的，不装作是原样命中。
     拼不出来就把目录定位开、把下一步写清楚 —— 目录展开后那一行就在眼前，
     要把文件新增进去，直接拖到那一行即可。 */
  function pendingPathDir(r, item, inp) {
    var dir = String(r.path || '').replace(/\/+$/, '');
    var name = item && item.fileName ? item.fileName : '';
    if (!name) {
      inp.value = r.path;
      revealByPath('app', r.path);
      setPathNote('<code>' + escapeHtml(r.path) + '</code> 是目录，不能当替换目标 —— ' +
        '请把路径粘到具体文件那一层。', true);
      return;
    }
    busy(true, '正在按目录查找…');
    call('ResolveAppPath', dir + '/' + name)
      .then(function (x) {
        busy(false, '');
        if (x && !x.missing && !x.dir) {
          inp.value = x.path;
          pendingSelect(item, x.path);
          setPathNote('粘的是目录，已按「目录 + ' + escapeHtml(name) + '」定位到 <code>' +
            escapeHtml(x.path) + '</code>。');
          toast('已定位到 ' + x.path);
          return;
        }
        inp.value = r.path;
        revealByPath('app', r.path);
        setPathNote('<b>' + escapeHtml(r.name || dir) + '</b> 是目录，里面没有 ' +
          escapeHtml(name) + '，所以不能直接替换。<br>' +
          '· 要替换某个文件：路径请粘到<b>文件</b>那一层；<br>' +
          '· 要把 ' + escapeHtml(name) + ' 新增到这个目录：把文件拖到应用树里刚定位的那一行。', true);
        toast('这是目录，不是文件 —— 已为你定位', true);
      })
      .catch(function (e) { busy(false, ''); toast(errText(e), true); });
  }

  /* 窗口按钮（最小化 / 最大化 / 关闭）+ 工具栏拖动区。
   *
   * 窗口是无边框的（gui/main.go 的 Frameless: true），原生标题栏连同它那三个按钮
   * 一起没了，所以这三个按钮搬到了工具栏右端（index.html 的 .win-ctls）。
   * 拖动靠 CSS（app.css 的 .toolbar { --wails-draggable: drag }），Wails 的运行时
   * 自己处理 mousedown/mousemove，这里只补"双击最大化"这一条原生行为。
   *
   * 走查（纯浏览器）里 window.runtime 是替身、没有这几个方法，所以逐个判空 ——
   * 直接调 undefined 会抛错，而 init() 里一处抛错会把后面所有初始化一起带崩
   * （这是本项目踩过的坑：一个 null 让无关断言连锁变红）。 */
  function initWindowControls() {
    var rt = window.runtime || {};
    var min = $('btn-win-min');
    var max = $('btn-win-max');
    var close = $('btn-win-close');
    var bar = document.querySelector('.toolbar');

    if (min) min.addEventListener('click', function () {
      if (rt.WindowMinimise) rt.WindowMinimise();
    });
    if (max) max.addEventListener('click', function () {
      if (rt.WindowToggleMaximise) rt.WindowToggleMaximise();
      setTimeout(syncWinMaxIcon, 60);            // 等 Wails 那边改完窗口状态再刷新图标
    });
    if (close) close.addEventListener('click', function () {
      if (rt.Quit) rt.Quit();
    });

    // 双击标题栏空白处 = 最大化/还原（原生标题栏的行为）。落在按钮上不算。
    if (bar) bar.addEventListener('dblclick', function (e) {
      if (e.target && e.target.closest && e.target.closest('button')) return;
      if (rt.WindowToggleMaximise) rt.WindowToggleMaximise();
      setTimeout(syncWinMaxIcon, 60);
    });

    syncWinMaxIcon();
  }

  /* 让最大化按钮的图标跟窗口真实状态一致（最大化时显示"还原"）。
   * 走查里没有 WindowIsMaximised，直接跳过 —— 按钮保持默认的"最大化"图标。 */
  function syncWinMaxIcon() {
    var btn = $('btn-win-max');
    if (!btn) return;
    var rt = window.runtime || {};
    if (!rt.WindowIsMaximised) return;
    rt.WindowIsMaximised().then(function (m) {
      btn.classList.toggle('is-max', !!m);
      btn.title = m ? '还原' : '最大化';
    }).catch(function () {});
  }

  /* 落点坐标 -> 面板标识（app / patch）。
   * 用浏览器自己的命中测试，别把布局换算搬到后端去猜。 */
  function paneAt(x, y) {
    var el = document.elementFromPoint(x, y);
    while (el) {
      if (el.dataset && el.dataset.side) return el.dataset.side;
      el = el.parentElement;
    }
    return null;
  }

  // ------------------------------------------------------------ 右键菜单

  /* 补丁树的右键菜单。
   *
   * **菜单内容全部由后端算好**（node.menu），前端只做两件事：
   *   1. 按顺序渲染（含单选勾选态、置灰、子菜单）；
   *   2. 按 id 分发到对应的后端动作。
   *
   * 为什么不在前端拼：每个菜单项的显示条件都要看父级、祖先链、两个包是否
   * 打开、自定义根的数量、该根下有没有 README —— 树是懒渲染的，父级可能
   * 还没建 DOM，前端拿不到。而且文案本身也会变（主根显示"重新加载"、
   * 自定义根显示"刷新信息"），单选类还带勾选态。
   *
   * 顺带说清这些项解决的原始问题：原版查找 README 时【只扫补丁根的直接
   * 子节点】，所以"README 套在一层目录里"或"条目名带 ./ 前缀"的包，自动
   * 流程认不出说明文件 —— 原版给的解法就是手动指定（标记根节点 /
   * 标记说明文件），这也是"重新加载 / 刷新信息"之外最常用的两项。 */
  function initContextMenu() {
    /* 两棵树共用一套：菜单内容由后端算好挂在 node.menu 上（见 gui/app.go 的
       patchMenu / appMenu），这里只负责弹出来。
       动作分发要知道点的是哪棵树 —— 同一个「保存文件」在两边都要能用，
       所以把 row.__tree 一路传到 dispatch。 */
    ['app-tree', 'patch-tree'].forEach(function (id) {
      var host = $(id);
      if (!host) return;
      host.addEventListener('contextmenu', function (e) {
        var row = e.target.closest ? e.target.closest('.row') : null;
        var node = row && row.__node;
        if (!node || !node.menu || !node.menu.length) return;  // 没有可用项就放行默认菜单
        e.preventDefault();
        openMenu(e.clientX, e.clientY, node, row.__tree);
      });
    });

    document.addEventListener('click', hideMenu);
    window.addEventListener('blur', hideMenu);
  }

  function hideMenu() {
    var m = $('ctxmenu');
    m.hidden = true;
    m.textContent = '';
  }

  function openMenu(x, y, node, treeKey) {
    openMenuItems(x, y, node.menu, function (it) { dispatch(it, node, treeKey); });
  }

  /* openMenuItems 在 (x,y) 处弹出菜单。
   * 菜单内容与"点了之后干什么"分开传 —— 补丁树右键和内容预览右键用的是
   * 同一套渲染，只是动作不同。 */
  function openMenuItems(x, y, items, onPick) {
    var host = $('ctxmenu');
    host.textContent = '';
    host.appendChild(buildMenu(items, onPick, false));
    host.hidden = false;

    // 贴到窗口右下边缘时把菜单挪回可视区内
    var w = host.offsetWidth, h = host.offsetHeight;
    host.style.left = Math.max(0, Math.min(x, window.innerWidth - w - 4)) + 'px';
    host.style.top = Math.max(0, Math.min(y, window.innerHeight - h - 4)) + 'px';
  }

  /* buildMenu 递归地把菜单描述渲染成 DOM。
   * sub 为真表示这是子菜单（样式上叠在父项右侧）。
   * onPick 收到被点中的那一项，由调用方决定怎么分发。 */
  function buildMenu(items, onPick, sub) {
    var box = document.createElement('div');
    box.className = 'ctxmenu' + (sub ? ' sub' : '');

    items.forEach(function (it) {
      var hasSub = !!(it.children && it.children.length);

      // 子菜单的父项不是按钮 —— 它只负责悬停展开，点了不该触发任何动作
      var el = document.createElement(hasSub ? 'div' : 'button');
      if (!hasSub) el.type = 'button';
      el.className = 'ctxitem' + (it.disabled ? ' disabled' : '') + (hasSub ? ' has-sub' : '');

      // 勾选位：单选类显示 ✓，其余留空占位，保证文字左对齐
      var check = document.createElement('span');
      check.className = 'ctx-check';
      check.textContent = it.radio && it.checked ? '\u2713' : '';
      el.appendChild(check);

      var label = document.createElement('span');
      label.className = 'ctx-label';
      label.textContent = it.label;
      el.appendChild(label);

      if (hasSub) {
        var arrow = document.createElement('span');
        arrow.className = 'ctx-arrow';
        arrow.textContent = '\u25B8';
        el.appendChild(arrow);
        el.appendChild(buildMenu(it.children, onPick, true));
      } else if (it.disabled) {
        el.disabled = true;
      } else {
        el.addEventListener('click', function (e) {
          e.stopPropagation();   // 别让 document 上那个"点空白处收起"顺手关掉
          hideMenu();
          onPick(it);
        });
      }

      box.appendChild(el);
    });

    return box;
  }

  /* dispatch 按菜单项 id 分发。id 的取值见 Go 侧的 menuAction* 常量。
   * treeKey 是 'app' / 'patch' —— 两棵树共用同一套 id，动作要靠它区分操作对象。 */
  function dispatch(it, node, treeKey) {
    switch (it.id) {
      case 'reload':
        return runState('ReloadOrRefresh', [treeKey, node.path], '已刷新');
      case 'cancelMapped':
        return runState('CancelMapped', [treeKey, node.path], '已取消关联');
      case 'markRoot':
        return runState('MarkRoot', [node.path],
          node.type === 'customRoot' ? '已取消根节点标记' : '已标记为根节点');
      case 'markReadme':
        return runState('MarkReadme', [node.path], '已设为说明文件');
      case 'editContent':
        return openEditor(treeKey, node);
      case 'saveFile':
        return runState('SaveNodeAs', [treeKey, node.path], '已保存');
      case 'saveSourceFile':
        // 导出的是反编译出来的 .java，不是 class 字节 —— 默认文件名也由
        // 后端把后缀换成 .java（见 Go 侧 sourceSaveName）。
        return runState('SaveSourceNodeAs', [treeKey, node.path], '已保存源文件');
      case 'copyName':
        return runCopy(treeKey, node.path, 'copyName', '名称');
      case 'copyPath':
        return runCopy(treeKey, node.path, 'copyPath', '路径');
      case 'copyRelativePath':
        return runCopy(treeKey, node.path, 'copyRelativePath', '相对路径');
      case 'copyMd5':
        return runCopy(treeKey, node.path, 'copyMd5', 'MD5');
      case 'delete':
        // 「删除」是把节点从应用树上物理摘掉，不可撤销 —— 原版弹确认框，这里照做。
        // 注意只有应用树有这一项（两棵树共用 dispatch），所以不用判 treeKey。
        return confirmBox({
          title: '删除文件',
          text: '将从应用树中移除「' + node.name + '」，统计记为手动删除。\n\n' +
                '标记为「手动删除」的文件不会写进输出包的删除清单。',
          okText: '删除',
          danger: true
        }).then(function (ok) {
          if (!ok) return;
          return runState('DeleteAppNode', [node.path], '已删除');
        });
      case 'markDelete':
        // 单选切换项：当前已是删除态就点成取消标记。
        return runState('MarkDeleteAppNode', [node.path],
          node.type === 'del' ? '已取消标记删除' : '已标记删除');
      case 'resetManualDel':
        return resetManualDel();
      case 'viewSign':
        return showSign(node.path);
      case 'viewNodeMapped':
        return showNodeMapped(node.path);
      case 'openInExplorer':
        return runVoid('OpenInExplorer', [treeKey, node.path], '已在资源管理器中打开');
      case 'closePatch':
        return runState('ClosePatchPack', [], '已关闭补丁包');
      case 'closeAppPack':
        return runState('CloseAppPack', [], '已关闭应用包');
      case 'focusPatchTree':
        return runState('SetFocusTree', [it.checked ? 0 : 2],
          it.checked ? '已取消聚焦' : '已聚焦补丁树');
      case 'focusAppTree':
        return runState('SetFocusTree', [it.checked ? 0 : 1],
          it.checked ? '已取消聚焦' : '已聚焦应用树');
      default:
        toast('未实现的菜单项：' + it.id, true);
        return Promise.resolve();
    }
  }

  /* 菜单动作统一收口，避免每项各写一遍 loading/报错/应用状态。
   * kind 区分后端返回的是完整状态还是无返回值。 */
  function runState(method, args, okMsg) {
    busy(true, '正在处理…');
    return call.apply(null, [method].concat(args))
      .then(function (s) { busy(false, ''); if (s) apply(s); if (okMsg) toast(okMsg); })
      .catch(function (e) { busy(false, ''); toast(errText(e), true); });
  }

  function runVoid(method, args, okMsg) {
    busy(true, '正在处理…');
    return call.apply(null, [method].concat(args))
      .then(function () { busy(false, ''); if (okMsg) toast(okMsg); })
      .catch(function (e) { busy(false, ''); toast(errText(e), true); });
  }

  function runCopy(treeKey, path, kind, what) {
    return call('CopyNodeText', treeKey, path, kind)
      .then(function () { toast('已复制' + what); })
      .catch(function (e) { toast(errText(e), true); });
  }

  function errText(e) { return String((e && e.message) || e); }

  // -------------------------------------------------- 查看签名 / 查看绑定信息

  function showSign(path) {
    return call('GetPatchSign', path)
      .then(function (s) {
        var body = $('sign-body');
        body.textContent = '';
        [['名称', s.name], ['MD5', s.md5], ['SHA1', s.sha1]].forEach(function (row) {
          var line = document.createElement('div');
          line.className = 'modal-row';
          var lab = document.createElement('label');
          lab.textContent = row[0] + '：';
          var inp = document.createElement('input');
          inp.type = 'text';
          inp.readOnly = true;   // 对齐原版：只读文本框
          inp.value = row[1] || '';
          line.appendChild(lab);
          line.appendChild(inp);
          body.appendChild(line);
        });
        $('sign-overlay').hidden = false;
      })
      .catch(function (e) { toast(errText(e), true); });
  }

  function showNodeMapped(path) {
    currentMappedPath = path;
    return call('GetNodeMappedText', path)
      .then(function (text) {
        $('mapped-body').textContent = text || '';
        $('mapped-copy').disabled = false;
        $('mapped-copy').textContent = '拷贝信息';
        $('mapped-overlay').hidden = false;
      })
      .catch(function (e) { toast(errText(e), true); });
  }

  var currentMappedPath = '';

  // ---------------------------------------------------------- 文件内容预览

  /* 对应原版一次 `new ContentView(node).showDialog(stage)`。
   *
   * 与其它几个弹窗不同，这个**不铺遮罩**：原版的对话框是 Modality.NONE
   * （非模态）且可缩放，用户开着预览还能继续点两棵树。所以这里做成一个
   * 可拖动、可缩放的浮动面板。
   *
   * 内容（文本、语法高亮、行号）**全部由后端算好**（internal/content），
   * 前端只负责拼 DOM 和分发右键菜单动作 —— 高亮规则与行号口径都有跨语言
   * 黄金基准管着，不能在 JS 里重写一份。 */
  var contentWin = null;     // 当前打开的预览（后端 ContentView + side/path）
  var contentSize = null;    // 面板尺寸与位置，对应原版持久化的 contentWidth/Height
  var contentMaxed = false;  // 面板是否处于最大化
  var contentRestore = null; // 最大化前的几何，供「还原」用（只在本次打开内有效）
  var contentPicked = {};    // 用户选过的编码：0 左栏 / 1 右栏

  function openContent(side, node) {
    /* .class 要先起一次 JVM 去反编译，明显比读文本慢（本机实测约 1 秒）。
       所以先把面板摆出来显示"正在反编译"，而不是让用户对着没反应的界面
       干等 —— 那看起来就像卡死了。其余类型不必多这一步。 */
    var slow = /\.class$/i.test(node.name || '');
    if (slow) {
      contentPicked = {};
      contentWin = { mode: 'loading', title: node.path, message: '正在反编译…', side: side, path: node.path };
      renderContent();
      showContentWin();
    }

    return call('OpenContent', { side: side, path: node.path })
      .then(function (v) {
        if (!v) return;
        // 没有解析器：原版把内容写到临时目录交给系统程序打开
        if (v.mode === 'external') {
          if (slow) hideContentWin();
          toast('已用系统默认程序打开');
          return;
        }
        // 反编译不可用（多半是没装 Java）等失败情形：收起加载面板再提示，
        // 别让"正在反编译…"一直挂在那里
        if (v.mode === 'unsupported') {
          if (slow) hideContentWin();
          toast(v.message || '该类型暂不支持预览', true);
          return;
        }
        contentPicked = {};
        v.side = side;
        v.path = node.path;
        contentWin = v;
        renderContent();
        showContentWin();
      })
      .catch(function (e) {
        if (slow) hideContentWin();
        toast(errText(e), true);
      });
  }

  /* 重新取数并重绘。
   *
   * 字符集切换与差异模式切换在原版里都是"重新 parse / 重新 build"，
   * 所以这里统一走一次 OpenContent。charset 为空表示不改编码。
   *
   * diffModel 传 undefined 表示不改。 */
  function reloadContent(charset, paneIndex, diffModel) {
    var v = contentWin;
    if (!v) return Promise.resolve();

    if (charset) {
      /* 差异（单栏）模式下原版这里会崩 —— ContentView.handleCharsetChange
         拿到的 node 是 null（那个 CodeArea 没绑节点），走 else 分支去
         parse(null) 直接 NPE。既然原版是坏的，这里选最有用的行为：
         两侧一起按新编码重解。双栏模式下则只改右键点中的那一栏，
         与原版一致。见 MIGRATION.md。 */
      if (v.mode === 'diff') {
        contentPicked[0] = charset;
        contentPicked[1] = charset;
      } else {
        contentPicked[paneIndex] = charset;
      }
    }

    /* 来源分流（三条路）：
         - 文件对比结果（fromFile）：两侧是磁盘上的两个普通文件，走 OpenFileCompare；
         - 包对比结果（fromCompare）：两侧是上次比的那两个包里的条目，走 OpenCompareContent；
         - 树里的节点：走 OpenContent。
       参数不一样 —— side 是树专有的，path 对树/包对比有意义，
       文件对比则用显式传进来的 fileLeft / fileRight。
       **这两个字段名不能叫 left / right**：后端 ContentView 的
       json:"left" / json:"right" 装的是 `{path, lines}` 两栏对象，
       叫一样的名字会把它们盖掉（详见 compareOpenFiles 的注释）。 */
    if (v.fromFile) {
      return call('OpenFileCompare', {
        left: v.fileLeft,
        right: v.fileRight,
        charset: contentPicked[0] || '',
        rightCharset: contentPicked[1] || '',
        diffModel: diffModel === undefined ? v.diffModel : diffModel
      })
        .then(function (nv) {
          if (!nv || nv.mode === 'external' || nv.mode === 'unsupported') return;
          nv.fromFile = true;
          nv.fileLeft = v.fileLeft;
          nv.fileRight = v.fileRight;
          contentWin = nv;
          renderContent();
        })
        .catch(function (e) { toast(errText(e), true); });
    }

    var fromCmp = !!v.fromCompare;
    var opt = {
      path: v.path,
      charset: contentPicked[0] || '',
      rightCharset: contentPicked[1] || '',
      diffModel: diffModel === undefined ? v.diffModel : diffModel
    };
    /* 对比来源要带嵌套路径链（展开过嵌套 jar 时条目在内层包里）；顶层不带
       （与后端 omitempty、走查里"只发条目路径"的断言一致）。树来源用 side。 */
    if (fromCmp) {
      if (v.chain && v.chain.length) opt.chain = v.chain;
    } else {
      opt.side = v.side;
    }

    return call(fromCmp ? 'OpenCompareContent' : 'OpenContent', opt)
      .then(function (nv) {
        if (!nv || nv.mode === 'external' || nv.mode === 'unsupported') return;
        nv.path = v.path;
        if (fromCmp) {
          nv.fromCompare = true;
          nv.chain = v.chain || [];
        } else {
          nv.side = v.side;
        }
        contentWin = nv;
        renderContent();
      })
      .catch(function (e) { toast(errText(e), true); });
  }

  function renderContent() {
    // 重渲染会换掉整个正文，查找条要跟着收（它挂在正文之外，不收会指向旧文本）
    findClose();
    // 正文马上就不是原来那份了：旧的镜像偏移表先作废。下面铺新镜像时会重新
    // 赋值；加载态等早退分支留着 null，renderFindHighlights 见到 null 就不画。
    mirrorInfo = null;
    var v = contentWin;
    $('content-title').textContent = v.title || '';
    $('content-title').title = v.title || '';
    // 预览模式不显示编辑按钮条（编辑模式由 renderEditor 打开它）
    $('content-foot').hidden = true;

    var body = $('content-body');
    body.textContent = '';
    body.className = 'contentwin-body' + (v.mode === 'split' ? ' split' : '');

    /* 差异导航：块列表随内容一起换（只有双栏才有）。计数回到 0/n、
       不高亮任何一处 —— 打开时不该假装"已经跳到第 1 处"（那处可能在
       几千行之外，屏幕上根本看不到）。 */
    contentBlocks = (v.mode === 'split' && v.blocks) ? v.blocks : [];
    contentNavIndex = -1;
    contentNavLockUntil = 0;
    updateContentNav();

    // 加载态：.class 要先起 JVM 反编译，给个明确的等待提示
    if (v.mode === 'loading') {
      var wait = document.createElement('div');
      wait.className = 'loading';
      wait.textContent = v.message || '正在加载…';
      body.appendChild(wait);
      return;
    }

    var leftPane = makePane(v.left, v.lang, 0);
    body.appendChild(leftPane);
    if (v.mode === 'split' && v.right) {
      var rightPane = makePane(v.right, v.lang, 1);
      body.appendChild(rightPane);
      // 双栏：两栏上下滚动同步（见 syncScroll）
      syncScroll(leftPane.querySelector('.code'), rightPane.querySelector('.code'));
      /* 手动滚动时更新"第几处"。只挂左栏：两栏同步滚动，左栏的事件足够
         （用户滚右栏时，同步会把左栏一起挪动，左栏也会发 scroll）。 */
      var navCode = leftPane.querySelector('.code');
      if (navCode) navCode.addEventListener('scroll', syncContentNavFromScroll);
    }

    /* 行号列底部补上横向滚动条的高度（见 fitGutterTail）。必须等这一帧布局
       完成才量得到滚动条厚度，所以推一拍。 */
    requestAnimationFrame(refitGutters);

    // 渲染完把焦点带进第一栏。原版 ContentView 是**模态**对话框，一打开焦点
    // 就在代码区里，Ctrl+F 自然是"面板内查找"；移植后面板是非模态的，且
    // renderContent 常跑在 showContentWin 之前（首次打开时面板还是
    // display:none，同步 focus 会静默失败），所以延一拍、等面板显示出来再聚焦。
    // renderContent 入口已 findClose()，偷焦点不会打断正在用的查找条；字符集 /
    // 差异模式切换时焦点回到第一栏也是想要的行为。
    var firstCode = leftPane.querySelector('.code');
    setTimeout(function () { firstCode.focus({ preventScroll: true }); }, 0);

    /* 预览模式：铺一层 hidden 的"文本镜像"，让 Ctrl+F 能搜预览正文（见
       findArea / renderFindHighlights 与 MIGRATION.md §五）。编辑器模式走
       renderEditor，不在这里铺镜像。镜像内容 = 两栏纯文本（split 用 \n\n 拼），
       与现有 findHits 的偏移口径完全一致，命中才能精确映射回渲染层。 */
    var mirror = document.createElement('textarea');
    mirror.className = 'edit-text mirror-text';
    mirror.hidden = true;
    mirror.spellcheck = false;
    var m0 = paneText(0);
    if (v.mode === 'split' && v.right) {
      mirror.value = m0 + '\n\n' + paneText(1);
      mirrorInfo = { split: true, p0Len: m0.length };
    } else {
      mirror.value = m0;
      mirrorInfo = { split: false, p0Len: m0.length };
    }
    body.appendChild(mirror);
  }

  /* 一栏代码区。index 用来区分左右（0/1），字符集切换要知道改的是哪一栏。 */
  function makePane(pane, lang, index) {
    var box = document.createElement('div');
    box.className = 'codepane';
    // 两个容器都带上 data-pane：一个给正文（.code，走 contentCodeEl），
    // 一个给整栏（.codepane，走 paneBoxEl —— 差异高亮要同时改行号列和正文列）
    box.dataset.pane = String(index);

    var parts = linesHtml(pane && pane.lines);

    /* 行号列。**自己不做横向滚动**，所以里面的 .ln 不需要 position:sticky
       —— 这正是把行号列拆出 .code 的目的（见 linesHtml 上面那段实测数据：
       一万个 sticky 元素让 10000 行的文件从 60fps 掉到 24fps）。
       纵向靠 syncGutter 跟着正文走。 */
    var gutter = document.createElement('div');
    gutter.className = 'code-gutter';
    gutter.innerHTML = parts.gutter;

    var code = document.createElement('div');
    // lang-* 是语法高亮的样式作用域：同一个类名（keyword 之类）在不同
    // 语言里颜色不同，原版靠"每个 CodeArea 各挂一份样式表"实现，
    // 这里用一层语言类代替。
    code.className = 'code lang-' + (lang || 'plain');
    // 代码栏要能拿焦点：面板内 Ctrl+F 的处理器挂在 #content-win 上，靠事件
    // 气泡到达（见 initFindBar）。只读 div 默认不可聚焦，用户在对比视图点开
    // 代码看、焦点却还在树上，Ctrl+F 就一路气泡到 document，弹的是主窗口的
    // 全局搜索对话框而不是面板内查找（需求 R）。tabIndex = 0 让点击/Tab 都能
    // 把焦点带进面板，与编辑模式"点进 textarea 焦点自然在面板里"对齐。
    code.tabIndex = 0;
    code.dataset.pane = String(index);
    code.innerHTML = parts.text;

    code.addEventListener('contextmenu', function (e) {
      e.preventDefault();
      e.stopPropagation();
      openContentMenu(e.clientX, e.clientY, index);
    });

    syncGutter(code, gutter);
    box.appendChild(gutter);
    box.appendChild(code);
    return box;
  }

  /* 行号列跟着正文列纵向滚。

     行号列自己是 `overflow: hidden`（不横滚），但内容比视口高，所以
     **仍然可以程序化地设 scrollTop** —— 浏览器只是不给你滚轮和滚动条。

     "赋成相同值不会再触发 scroll 事件"，所以不会回环，不需要加锁
     （与 syncScroll 同一招，那边的注释详述了为什么不能用标志位）。 */
  function syncGutter(code, gutter) {
    code.addEventListener('scroll', function () {
      if (gutter.scrollTop !== code.scrollTop) gutter.scrollTop = code.scrollTop;
    });
  }

  /* 把行号列的可滚范围补齐到与正文列一致。

     为什么需要：`.code` 是 `overflow: auto`，内容超宽时会**在底部占掉一条
     横向滚动条**（Windows 上 15px）；行号列是 `overflow: hidden`，没有这条，
     于是两列的 `scrollHeight - clientHeight` 差 15px —— 正文能多滚 15px，
     行号列滚不到，**滚到文件末尾时行号会比代码低一行**。

     补法：把横向滚动条占掉的高度作为 padding 补到行号列底部。用 padding
     而不是改 scrollHeight 的其他手段，是因为它加在内容**之后**，不会挪动
     任何一行的位置。`offsetHeight - clientHeight` 就是滚动条厚度
     （覆盖式滚动条 / 没有横向滚动条时是 0，此时不补）。

     必须在元素进 DOM、完成布局之后量，所以调用点放在 appendChild 之后。 */
  function fitGutterTail(code) {
    var gut = code.previousElementSibling;
    if (!gut || !gut.classList.contains('code-gutter')) return;
    var sb = code.offsetHeight - code.clientHeight;
    gut.style.paddingBottom = (sb > 0 ? sb : 0) + 'px';
  }

  /* 对正文里所有代码栏重量一次。窗口尺寸变了（最大化 / 拖拽）横向滚动条
     可能出现或消失，厚度跟着变，所以要重算，不能只在渲染时算一次。 */
  function refitGutters() {
    var body = $('content-body');
    if (!body) return;
    var codes = body.querySelectorAll('.code');
    for (var i = 0; i < codes.length; i++) fitGutterTail(codes[i]);
  }

  /* syncScroll 让双栏的两栏上下滚动同步。

     **原版没有这个行为**：ContentView.dynamicCodeAreaWithDiffModel 的双栏分支
     只是把两个 VirtualizedScrollPane 摆进 SplitPane，两边各滚各的
     （属刻意增强，见 MIGRATION.md §一）。

     只同步纵向（scrollTop）—— 用户要的是"上下滑动左右同步"，横向各栏按自己
     的行宽来更合理。

     两个实现上的坑，别改回去：

     1. **不能用锁（flag / rAF）来防回环。** 试过"程序化同步时上锁、rAF 解锁"
        的写法：无头浏览器里 rAF 会被饿死（--virtual-time-budget 下不产帧），
         锁卡在 true，反向同步直接失效。这里改成**不需要锁** —— 赋值成相同
         值不会再触发 scroll 事件，回环天然终止。
     2. **目标栏滚不到那么远时不要硬同步。** 两栏内容长度不一样时，浏览器会把
        目标栏夹到它自己的最大值，那一次 scroll 事件又会把源栏拽回来 ——
        结果长的那一栏永远滚不过短栏的底部。所以先比一下目标的极限，超了就
        不同步（短栏停在底部，长栏继续走）。 */
  function syncScroll(a, b) {
    if (!a || !b) return;
    /* 行号列就在 .code 前面（makePane 里先 appendChild(gutter) 再 code），
       所以用 previousElementSibling 直接拿到，不必再传参。 */
    function gutterOf(code) {
      var p = code.previousElementSibling;
      return (p && p.classList.contains('code-gutter')) ? p : null;
    }
    function pullGutter(code, gut) {
      if (gut && gut.scrollTop !== code.scrollTop) gut.scrollTop = code.scrollTop;
    }
    function mirror(src, dst, gdst) {
      src.addEventListener('scroll', function () {
        var max = dst.scrollHeight - dst.clientHeight;
        if (src.scrollTop > max) return;
        if (dst.scrollTop !== src.scrollTop) {
          dst.scrollTop = src.scrollTop;
          /* ⚠️ **必须在这里同步把对方的行号列也推过去**，不能指望 dst 自己的
             scroll 事件。scroll 事件是**异步**派发的：设了 dst.scrollTop 之后，
             它的处理函数要等到下一帧才跑，于是 dst 的行号列整整慢一帧。

             实测（.workbuddy-ai/mockups/probe_sync_desync.js，每帧滚 60px）：
             只靠各自的事件时，被镜像那一栏的行号列**每帧都落后 120px**
             （≈7 行），快速滚动时行号指向的就不是屏幕上的代码。用户
             2026-10-26 报障"按住上下快速滑动时，会短暂出现这个情况"。
             在这里当场推一次之后，实测最大脱节降到 0。

             不会回环：dst 的事件下一帧到达时，它自己的 syncGutter 看到
             scrollTop 已经相等，直接跳过；mirror 反向赋值也是同值，不再派发。 */
          pullGutter(dst, gdst);
        }
      });
    }
    mirror(a, b, gutterOf(b));
    mirror(b, a, gutterOf(a));
  }

  /* --------------------------------------------- 双栏差异导航（上一处 / 下一处）

     为什么需要：对比结果里差异可能只有几处、却散在几千行中，用户得一路
     上下翻着找（用户原话："差异处不太明显，得上下翻动才能查看不同处"）。
     行号列的 +/-/~ 符号只能告诉你**屏幕上这几行**是不是差异，回答不了
     "整份文件的差异都在哪" —— 所以补一对跳转按钮。

     差异块由后端给（content.SplitBlock，见 ComputeSplitMarks）：
     "哪几行属于同一处差异"是差异算法的知识（一个"改"块里左第 k 行配右
     第 k 行），前端自己扫 DOM 猜等于把这个口径复制一份，迟早漂移。 */

  /* 当前这份内容的差异块与"停在第几处"。-1 = 还没跳过任何一处
     （计数显示 0/n，按"下一个"从第 1 处开始）。 */
  var contentBlocks = [];
  var contentNavIndex = -1;

  /* 上一次跳转的时刻。跳转会触发 scroll 事件，若不理会，滚动同步会把计数
     改成"离视口顶部最近的那一块"，与刚点的那块对不上（跳转用
     block:'center'，块的起始行在中线偏上）。所以跳转后短暂忽略滚动同步。

     用时间戳而不是布尔标志：**scroll 事件是异步派发的**，同步置位、同步
     复位的话，事件到达时标志已经复位了，等于没加（与 syncScroll 里
     "不能用锁"是同一类坑，那边靠"赋相同值不再触发事件"绕开）。 */
  var CONTENT_NAV_LOCK_MS = 250;
  var contentNavLockUntil = 0;

  /* .code 的行高（见 app.css 的 `.code { font: 14px/17px ... }`）。
     行高固定、正文 white-space: pre 不折行，所以"第 n 行的纵向位置 = n ×
     行高"是精确的。滚动事件里**不量 DOM** —— 每帧量几百个元素的
     getBoundingClientRect 会逼着浏览器反复重排。 */
  var CONTENT_LINE_H = 17;

  function contentCodeEl(index) {
    var body = $('content-body');
    return body ? body.querySelector('.code[data-pane="' + index + '"]') : null;
  }

  /* 整一栏（.codepane）—— 里面同时住着 .code-gutter 与 .code。
     差异高亮要同时改这两列，所以拿的是外层那个（见 markBlockSideCurrent）。 */
  function paneBoxEl(index) {
    var body = $('content-body');
    return body ? body.querySelector('.codepane[data-pane="' + index + '"]') : null;
  }

  /* 差异块的"参考行"落在哪一栏哪一行：优先左栏（左栏是原文那一侧，
     面板打开时焦点也在它上面）。只有右栏有的块（新增）才落到右栏。 */
  function blockRefPane(b) { return b.leftStart >= 0 ? 0 : 1; }
  function blockRefRow(b) { return b.leftStart >= 0 ? b.leftStart : b.rightStart; }

  function updateContentNav() {
    var nav = $('content-nav');
    if (!nav) return;
    var n = contentBlocks.length;
    /* 没有差异块就整组隐藏 —— 单栏 / 差异模式 / 两份完全一样时都不该出现
       （那两种形态的行号列本来就不带差异标记，没有可跳的目标）。 */
    nav.hidden = n === 0;
    var cnt = $('content-nav-count');
    if (cnt) cnt.textContent = n ? (contentNavIndex + 1) + '/' + n : '';
    var prev = $('content-nav-prev'), next = $('content-nav-next');
    /* 到头就置灰，**不绕回**：绕回会让"点了几下"与"文件里第几处"失去对应。 */
    if (prev) prev.disabled = n === 0 || contentNavIndex <= 0;
    if (next) next.disabled = n === 0 || contentNavIndex >= n - 1;
  }

  /* 给某一栏的第 [start, start+count) 行挂 .nav-current（描边高亮）。

     **行号列与正文列都要挂**：一行拆成 .gline 与 .cline 两个元素之后，
     只标一边的话框只画一半，看着像"这一行只有正文变了、行号没变"。 */
  function markBlockSideCurrent(paneIndex, start, count) {
    if (start < 0 || count <= 0) return;
    var box = paneBoxEl(paneIndex);
    if (!box) return;
    var rows = [
      box.querySelector('.code-gutter'),
      box.querySelector('.code')
    ];
    for (var r = 0; r < rows.length; r++) {
      var kids = rows[r] ? rows[r].children : [];
      for (var k = start; k < start + count && k < kids.length; k++) {
        kids[k].classList.add('nav-current');
      }
    }
  }

  function markBlockCurrent(i) {
    var body = $('content-body');
    if (!body) return;
    var old = body.querySelectorAll('.nav-current');
    for (var k = 0; k < old.length; k++) old[k].classList.remove('nav-current');
    if (i < 0 || i >= contentBlocks.length) return;
    var b = contentBlocks[i];
    /* 两侧都标：两栏是同一处差异的两个视角，只标一侧会让人以为另一边没变。 */
    markBlockSideCurrent(0, b.leftStart, b.leftCount);
    markBlockSideCurrent(1, b.rightStart, b.rightCount);
  }

  function scrollToContentBlock(i) {
    var b = contentBlocks[i];
    if (!b) return;
    var code = contentCodeEl(blockRefPane(b));
    if (!code) return;
    var row = code.children[blockRefRow(b)];
    if (!row || !row.scrollIntoView) return;
    /* 只滚这一栏：两栏的 scrollTop 是同步的（见 syncScroll），另一栏会跟着
       走。block:'center' 让那一处落在视口中部，上下都留出上下文。 */
    row.scrollIntoView({ block: 'center', inline: 'nearest' });
  }

  function contentNavGo(delta) {
    var n = contentBlocks.length;
    if (!n) return;
    var i = contentNavIndex + delta;
    if (i < 0) i = 0;
    if (i > n - 1) i = n - 1;
    contentNavIndex = i;
    contentNavLockUntil = Date.now() + CONTENT_NAV_LOCK_MS;
    scrollToContentBlock(i);
    markBlockCurrent(i);
    updateContentNav();
  }

  /* 手动滚动时把"第几处"跟着更新 —— 否则计数停在最后一次点按钮的位置，
     与眼前看到的内容对不上。取"离视口顶部最近的那一块"。

     只跟左栏的 scrollTop：两栏是同步滚动的，且参考行用的是"左栏行号"
     （只有右栏有的块也换算成同一套纵向位置，因为两栏共享 scrollTop）。 */
  function syncContentNavFromScroll() {
    if (!contentBlocks.length) return;
    if (Date.now() < contentNavLockUntil) return;
    var code = contentCodeEl(0);
    if (!code) return;
    var topRow = code.scrollTop / CONTENT_LINE_H;
    var best = 0, bestD = Infinity;
    for (var i = 0; i < contentBlocks.length; i++) {
      var d = Math.abs(blockRefRow(contentBlocks[i]) - topRow);
      if (d < bestD) { bestD = d; best = i; }
    }
    if (best !== contentNavIndex) {
      contentNavIndex = best;
      markBlockCurrent(best);
      updateContentNav();
    }
  }

  function escapeHtml(s) {
    return String(s == null ? '' : s).replace(/[&<>"]/g, function (c) {
      return c === '&' ? '&amp;' : c === '<' ? '&lt;' : c === '>' ? '&gt;' : '&quot;';
    });
  }

/* 把后端给的行拼成 DOM。后端返回的是"行号 + 若干文本片段"，
   片段直接对应一个 <span>（没有类名的就是纯文本）。

   mark 是双栏预览的差异标记（mod/add/del，空串表示无差异），
   落成 .gline / .cline 上的 mark-* 类；CSS 只管着色，语义全在后端。

   **返回两段 HTML：gutter（行号列）与 text（正文列）**，分别住进
   .code-gutter 与 .code 两个容器。

   为什么一行要拆成两个容器：行号列原来是 .code 里每行一个
   `position: sticky` 的 .ln。一万行就是一万个 sticky 元素，而 sticky 的
   包含块是滚动容器本身 —— 滚动时浏览器每帧都要在主线程上把这一万个
   重新排一遍。实测（.workbuddy-ai/mockups/probe_scroll_frames.js，
   10000 行）：平均一帧 39ms、74% 的帧超时，也就是 24fps，用户看到的
   "上下滑动卡顿"就是这个。把 sticky 去掉立刻回到 18.8ms；而拆成
   "不横滚的行号列 + 横滚的正文列"之后，一个 sticky 都不需要 ——
   行号列天然钉在左边，横滚时也不会跑掉。 */
function linesHtml(lines) {
  if (!lines || !lines.length) return { gutter: '', text: '' };
  var g = [], t = [];
  for (var i = 0; i < lines.length; i++) {
    var l = lines[i];
    var segs = l.segs || [];
    var s = '';
    for (var j = 0; j < segs.length; j++) {
      var txt = escapeHtml(segs[j].text);
      var cls = segs[j].class;
      // 类名只可能是后端那几个常量，白名单过滤一下更保险。
      // 允许**两段**：语法类 + 行内差异类，例如 "keyword mk-mod"
      // （见 content.SplitMarks —— 变了的词染状态色，其余保留语法着色）。
      s += (cls && /^[a-z][a-z0-9-]*( [a-z][a-z0-9-]*)?$/.test(cls))
        ? '<span class="' + cls + '">' + txt + '</span>'
        : txt;
    }
    var mark = (l.mark && /^(mod|add|del)$/.test(l.mark)) ? ' mark-' + l.mark : '';
    // 两侧用同一个 mark：差异行的底色要横跨行号列与正文列，只染一半会断成两截
    g.push('<div class="gline' + mark + '"><span class="ln">' +
      escapeHtml(l.num) + '</span></div>');
    t.push('<div class="cline' + mark + '"><span class="lt">' + s + '</span></div>');
  }
  return { gutter: g.join(''), text: t.join('') };
}

  function paneEl(index) {
    return $('content-body').querySelector('.code[data-pane="' + index + '"]');
  }

  /* 取某一栏的纯文本，口径对齐 JavaFX 的 CodeArea.getText()：段落用 \n 连。 */
  function paneText(index) {
    var v = contentWin;
    var pane = index === 1 ? v.right : v.left;
    if (!pane || !pane.lines) return '';
    return pane.lines.map(function (l) {
      return (l.segs || []).map(function (s) { return s.text; }).join('');
    }).join('\n');
  }

  // --------------------------------------------------------------- 编辑内容
  //
  /* **原版没有这个功能**：CodeAreaFactory.getContextMenu 里那段「编辑模式」
     菜单项被整段注释掉了（连同 ContentView.handleEditMode 里的 setEditable），
     codeArea 一直是 setEditable(false)。原版唯一能改包内内容的路径是 READ ME
     编辑器，只针对说明文件。属刻意增强，见 MIGRATION.md。

     三个形态由后端决定（gui/editor.go 的 OpenEditor）：
       text   —— 文本文件，等宽文本域直接改
       hex    —— 按字节编辑（十六进制网格）
       source —— .class 反编译出来的 Java 源码。**默认就是这个**：字节码本身
                 "改"不出有意义的结果。反编译不可用（本机没装 Java）时自动
                 退回 hex，不会因为没装 Java 就打不开。

     改动**只存在内存里**，点「保存」才写回节点；重新加载 / 重新匹配会丢弃
     （那会重建整棵树）。这是刻意定的语义，不是漏做。
     source 形态默认不能存回 class —— 源码要经 javac 配合完整依赖重新编译
     才能变回 class —— 所以没有「保存」，只有「编译为 .class」（有 JDK 时）
     或「另存为 .java」。*/

  var HEX_PAGE = 512;   // 十六进制每页字节数（512 = 32 行 × 16 列）

  function openEditor(treeKey, node, asBytes) {
    return call('OpenEditor', { side: treeKey, path: node.path, asBytes: !!asBytes })
      .then(function (v) {
        if (!v) return;
        adoptEditorView(v, treeKey);
        renderEditor();
        showContentWin();
      })
      .catch(function (e) { toast(errText(e), true); });
  }

  /* adoptEditorView 把后端返回的编辑器数据挂成当前面板状态。
     换编码时会再来一次，所以单独抽出来。 */
  function adoptEditorView(v, treeKey) {
    v.isEditor = true;
    v.treeKey = treeKey;
    v.dirty = false;
    v.hexPage = 0;
    v.bytes = v.mode === 'hex' ? base64ToBytes(v.base64 || '') : null;
    contentWin = v;
  }

  function renderEditor() {
    // 同上：换掉正文就把查找条收起来
    findClose();
    // 编辑器正文里没有镜像 textarea，旧的 mirrorInfo 一并作废（它的生命周期
    // 跟着"当前正文"走，见 findClose 里的注释）
    mirrorInfo = null;
    var v = contentWin;
    var body = $('content-body');
    body.textContent = '';
    body.className = 'contentwin-body edit';

    if (v.mode === 'hex') {
      body.appendChild(buildHexEditor(v));
    } else {
      // source（.class 反编译出来的 Java 源码）与 text 共用文本域，区别只在
      // 底部按钮与提示：源码不能存回包，所以没有编码栏、也没有「保存」。
      body.appendChild(buildTextEditor(v, v.mode !== 'source'));
    }

    $('content-foot').hidden = false;
    setEditorError(''); // 打开/重渲染时清掉上一次的报错残留
    syncEditorFoot();
    updateEditHint();
  }

  /* syncEditorFoot 按形态摆底部按钮。
     source（class 源码）不能按普通保存存回包 —— 源码要经 javac 配合完整依赖
     重新编译才能变回 class —— 所以隐藏「保存」，只留「编译为 .class」
     （有 JDK 时）与「另存为 .java」；同时给出「改字节」入口，因为有时目标
     只是替换一段字符串常量，那十六进制更直接。 */
  function syncEditorFoot() {
    var v = contentWin;
    if (!v || !v.isEditor) return;
    var isSource = v.mode === 'source';
    $('content-save').hidden = isSource;
    $('content-bytes').hidden = !isSource;
    $('content-compile').hidden = !(isSource && v.canCompile);
    $('content-saveas').textContent = isSource ? '另存为 .java' : '另存为';
  }

  /* updateEditHint 刷新标题与底部提示。
     "有未保存的修改"这个状态必须一眼可见 —— 否则用户关掉面板才发现白改了。 */
  function updateEditHint() {
    var v = contentWin;
    if (!v || !v.isEditor) return;

    var text;
    if (v.mode === 'source') {
      text = (v.dirty ? '● 有未保存的修改' : '○ 未修改')
        + ' · Java 源码 · 由 class 反编译';
      if (v.canCompile) {
        text += ' · 改完用「编译为 .class」或「另存为 .java」';
      } else if (v.compileHint) {
        text += ' · ' + v.compileHint + '，请「另存为 .java」导出';
      } else {
        text += ' · 改动不能存回 class（需 javac 重新编译），请「另存为 .java」导出';
      }
    } else {
      var modeName = v.mode === 'hex' ? '十六进制' : '文本';
      text = (v.dirty ? '● 有未保存的修改' : '○ 未修改')
        + ' · ' + modeName + '编辑 · ' + humanSize(v.size || 0)
        + ' · 改动只在本会话内有效，重新加载会丢弃';
    }
    var hint = $('content-edithint');
    hint.textContent = text;
    // 文字可能在窄窗口被省略号截断，把完整版挂到 title 上，hover 即可看全。
    hint.title = text;
    hint.className = 'hint' + (v.dirty ? ' dirty' : '');

    var title = (v.dirty ? '* ' : '') + (v.title || '');
    $('content-title').textContent = title;
    $('content-title').title = title;
  }

  // --------------------------------------- 内容面板内查找 / 跳转行列

  /* 对应原版 `control/code/SearchBox` 与 `GotoRowColDialog` —— 由
     `CodeEditor.onKeyPressed` 里的 **Ctrl+F / Ctrl+G** 唤起。

     原版的 `CodeEditor` 既是预览面板（`NodeCodeEditor`）也是编辑面板的基类，
     所以两个模式都响应 Ctrl+F：

       · 编辑模式 —— 直接在可见的 `.edit-text` textarea 上做选区（原生行为）。
       · 预览模式 —— 只读的 DOM 行渲染没有 textarea，所以 renderContent 会在
         content-body 里铺一个 **hidden 的镜像 textarea**（.edit-text.mirror-text，
         内容为两栏纯文本，split 两栏用 \n\n 拼），让现有 findArea / findHits /
         findCount / findStep 的**全部数学逻辑零改动**继续工作；命中再靠
         CSS Custom Highlight API（CSS.highlights + ::highlight）画到渲染层上
         —— 不改动 DOM，从而保住反编译代码的语法着色。见 MIGRATION.md §五。 */

  var findState = { open: false, keyword: '', hits: [], index: 0 };
  /* 预览镜像的元信息：split 模式两栏用 \n\n 分隔，p0Len 是第一栏文本长度，
     用来把全局命中偏移拆回"哪一栏 / 栏内第几行"。编辑器模式恒为 null。 */
  var mirrorInfo = null;
  var gotoPos = null;

  function findArea() {
    return $('content-win').querySelector('.edit-text');
  }

  /* 找出所有命中。逐条对齐原版 SearchBox.searchKeyword：
     从左到右、**不重叠**（找到一处后从命中末尾继续找）。 */
  function findHits(text, keyword) {
    var hits = [];
    if (!text || !keyword) return hits;
    var i = 0;
    while (i < text.length) {
      i = text.indexOf(keyword, i);
      if (i === -1) break;
      hits.push({ start: i, end: i + keyword.length });
      i += keyword.length;
    }
    return hits;
  }

  /* 把某一行滚到可视区中间，对应原版 `showParagraphAtCenter`。
     textarea 没有等价 API，这里按"行号 × 平均行高"估算 —— 开了自动折行时
     会有偏差，够用（原版是精确的段落定位）。 */
  function findScrollTo(ta, offset) {
    var before = ta.value.slice(0, offset);
    var line = before.split('\n').length - 1;
    var total = ta.value.split('\n').length;
    var lineH = ta.scrollHeight / Math.max(1, total);
    ta.scrollTop = Math.max(0, (line + 0.5) * lineH - ta.clientHeight / 2);
  }

  function findCount() {
    var el = $('find-count');
    if (!el) return;
    el.textContent = findState.hits.length
      ? (findState.index + 1) + '/' + findState.hits.length
      : '0 结果';
  }

  function findBtnState() {
    var dis = findState.hits.length === 0;
    var p = $('find-prev'), n = $('find-next');
    if (p) p.disabled = dis;
    if (n) n.disabled = dis;
  }

  function findSelect() {
    var ta = findArea();
    if (!ta) return;
    var hit = findState.hits[findState.index];
    if (!hit) { findCount(); return; }
    ta.setSelectionRange(hit.start, hit.end);
    findScrollTo(ta, hit.end);
    findCount();
    renderFindHighlights();
  }

  /* 重算并选中。挑哪一个是按原版 updateSearchResult 的口径：
     **从 caret 位置往后第一个 `start >= caret` 的命中**，找不到就回到第一个
     （原版那条链表是环形的，转一圈自然回到头）。 */
  function findUpdate(keepIndex) {
    var ta = findArea();
    if (!ta) return;
    findState.keyword = $('find-input').value;
    findState.hits = findHits(ta.value, findState.keyword);
    if (!findState.hits.length) {
      findState.index = 0;
      findCount(); findBtnState();
      return;
    }
    if (!keepIndex) {
      var caret = ta.selectionStart, pick = 0;
      for (var i = 0; i < findState.hits.length; i++) {
        if (findState.hits[i].start >= caret) { pick = i; break; }
      }
      findState.index = pick;
    }
    if (findState.index >= findState.hits.length) findState.index = 0;
    findSelect();
    findBtnState();
  }

  function findStep(delta) {
    var n = findState.hits.length;
    if (!n) return;
    findState.index = ((findState.index + delta) % n + n) % n;
    findSelect();
  }

  function findOpen() {
    var ta = findArea();
    if (!ta || !$('content-find')) return;
    $('content-find').hidden = false;
    findState.open = true;
    /* 打开时把代码区选中的文本回填到搜索框（原版 SearchBox.start）。
       跨行的选区不回填 —— 搜索框是单行输入。 */
    var sel = ta.value.slice(ta.selectionStart, ta.selectionEnd);
    if (sel && sel.indexOf('\n') < 0 && sel !== findState.keyword) {
      $('find-input').value = sel;
    }
    $('find-input').focus();
    $('find-input').select();
    findUpdate(false);
  }

  function findClose() {
    findState.open = false;
    findState.keyword = '';
    findState.hits = [];
    findState.index = 0;
    // 清掉预览模式的命中高亮（见 renderFindHighlights）
    if (window.CSS && CSS.highlights) {
      CSS.highlights.delete('find-hit');
      CSS.highlights.delete('find-active');
    }
    /* mirrorInfo 描述的是"当前正文渲染层"的结构，不是查找状态 —— 不能在这里
       清掉：关掉查找条再按 Ctrl+F 重开时，renderFindHighlights 还指着它映射
       命中偏移。曾在这里置 null，导致第二次打开查找条只剩计数、没有高亮
       （需求 R 走查暴露）。它的生命周期跟着正文走：renderContent / renderEditor
       换正文时各自维护。 */
    var bar = $('content-find');
    if (bar) bar.hidden = true;
    var inp = $('find-input');
    if (inp) inp.value = '';
    findCount();
    findBtnState();
  }

  /* 预览模式：把当前命中（findState.hits）画到只读渲染层上。
     用 CSS Custom Highlight API（CSS.highlights + ::highlight），**不改动 DOM**，
     从而保留反编译代码的语法着色（token span 的颜色还在）。
     命中跨行时按行拆成多个 Range；双栏 split 的两栏都搜、各自染色（镜像把两栏
     用 \n\n 拼成一段，命中落在哪一栏就染哪一栏）。编辑器模式不走到这里
     （findArea 返回的是可见 textarea，下方直接 return）。 */
  function renderFindHighlights() {
    var ta = findArea();
    // 只有预览模式的 hidden 镜像才需要在这套 DOM 上染色
    if (!ta || !ta.hidden) {
      clearFindHighlights();
      return;
    }
    clearFindHighlights();
    if (!window.CSS || !CSS.highlights || !mirrorInfo || !findState.hits.length) return;

    var hitRanges = [], activeRanges = [];
    for (var h = 0; h < findState.hits.length; h++) {
      collectRanges(findState.hits[h].start, findState.hits[h].end,
        h === findState.index ? activeRanges : hitRanges);
    }
    if (hitRanges.length) CSS.highlights.set('find-hit', makeHighlight(hitRanges));
    if (activeRanges.length) CSS.highlights.set('find-active', makeHighlight(activeRanges));
    scrollToActiveHit();
  }

  function clearFindHighlights() {
    if (window.CSS && CSS.highlights) {
      CSS.highlights.delete('find-hit');
      CSS.highlights.delete('find-active');
    }
  }

  function makeHighlight(ranges) {
    var h = new Highlight();
    for (var i = 0; i < ranges.length; i++) h.add(ranges[i]);
    return h;
  }

  /* 把全局偏移 [s,e) 按行拆成若干 DOM Range，推入 target。
     双栏 split：镜像 = 栏0 + '\n\n' + 栏1，所以先按 p0Len(+分隔) 决定落在哪一栏，
     再在该栏内按行拆分（行用单 \n 连接）。 */
  function collectRanges(s, e, target) {
    var map = buildLineMap();
    if (!map) return;
    var L0 = mirrorInfo.p0Len;
    var sep = mirrorInfo.split ? 2 : 0;
    if (s < L0) pushPaneRanges(map[0], s, Math.min(e, L0), target);
    if (e > L0 + sep) {
      pushPaneRanges(map[1], Math.max(s, L0 + sep) - (L0 + sep), e - (L0 + sep), target);
    }
  }

  /* 取两栏每行的 .lt 文本与"栏内"偏移起点。 */
  function buildLineMap() {
    var res = [null, null];
    for (var p = 0; p <= 1; p++) {
      var code = paneEl(p);
      if (!code) continue;
      var clines = code.querySelectorAll('.cline');
      var lines = [], acc = 0;
      for (var i = 0; i < clines.length; i++) {
        var lt = clines[i].querySelector('.lt');
        var txt = lt ? lt.textContent : '';
        lines.push({ lt: lt, start: acc, len: txt.length });
        acc += txt.length + 1;   // +1 = 行分隔符 \n
      }
      res[p] = { lines: lines };
    }
    return res;
  }

  /* 在某一栏的 lines（栏内偏移）里，把 [ls,le) 按行拆成 Range 推入 target。 */
  function pushPaneRanges(pane, ls, le, target) {
    if (!pane || !pane.lines || le <= ls) return;
    var pos = 0;
    for (var i = 0; i < pane.lines.length; i++) {
      var lineStart = pos;
      var lineEnd = pos + pane.lines[i].len;
      if (le <= lineStart) break;
      if (ls < lineEnd) {
        var ss = Math.max(ls, lineStart) - lineStart;
        var ee = Math.min(le, lineEnd) - lineStart;
        var r = rangeForOffsets(pane.lines[i].lt, ss, ee);
        if (r) target.push(r);
      }
      pos = lineEnd + 1;   // 跳到下一行起点（含 \n）
    }
  }

  /* 在 lt 元素内，按"栏内文本坐标"[start,end) 造一个 Range，跨 span 也行。 */
  function rangeForOffsets(lt, start, end) {
    if (!lt) return null;
    var range = document.createRange();
    var tw = document.createTreeWalker(lt, NodeFilter.SHOW_TEXT, null);
    var pos = 0, started = false;
    while (tw.nextNode()) {
      var tn = tw.currentNode;
      var tl = tn.nodeValue.length;
      if (!started && pos + tl >= start) {
        range.setStart(tn, Math.max(0, start - pos));
        started = true;
      }
      if (started && pos + tl >= end) {
        range.setEnd(tn, Math.min(tl, end - pos));
        break;
      }
      pos += tl;
    }
    if (!started) {
      // 整个区间落在文本之后（异常兜底）：放在末尾
      range.setStart(lt, lt.childNodes.length);
      range.setEnd(lt, lt.childNodes.length);
    } else if (!range.endContainer) {
      range.setEnd(lt, lt.childNodes.length);
    }
    return range;
  }

  /* 把当前 active 命中滚进可视区（block:nearest）。双栏的 syncScroll 会把
     另一栏镜像到相同 scrollTop。 */
  function scrollToActiveHit() {
    if (!findState.hits.length || !mirrorInfo) return;
    var hit = findState.hits[findState.index];
    var L0 = mirrorInfo.p0Len, sep = mirrorInfo.split ? 2 : 0;
    var pane, local;
    if (hit.start < L0) { pane = 0; local = hit.start; }
    else { pane = 1; local = hit.start - (L0 + sep); }
    var map = buildLineMap();
    var pl = map[pane] ? map[pane].lines : null;
    if (!pl) return;
    var pos = 0, target = null;
    for (var i = 0; i < pl.length; i++) {
      if (local < pos + pl[i].len) { target = pl[i]; break; }
      pos += pl[i].len + 1;
    }
    if (target && target.lt) {
      var cline = target.lt.closest('.cline');
      if (cline) cline.scrollIntoView({ block: 'nearest' });
    }
  }

  // ---- 跳转行列（Ctrl+G，对应原版 GotoRowColDialog）----

  function gotoLineStart(text, row) {
    var off = 0;
    for (var i = 0; i < row; i++) {
      var nl = text.indexOf('\n', off);
      if (nl === -1) return -1;
      off = nl + 1;
    }
    return off;
  }

  /* "行:列" 或 "行" -> 偏移。越界返回 -1。 */
  function gotoOffsetOf(text, row, col) {
    var start = gotoLineStart(text, row);
    if (start < 0) return -1;
    var nl = text.indexOf('\n', start);
    var lineLen = (nl === -1 ? text.length : nl) - start;
    if (col < 0 || col > lineLen) return -1;
    return start + col;
  }

  function gotoMark(which, bad) {
    var el = $(which === 'rowcol' ? 'goto-rowcol' : 'goto-offset');
    if (el) el.classList.toggle('invalid', !!bad);
  }

  function gotoClearMark() {
    gotoMark('rowcol', false);
    gotoMark('offset', false);
  }

  /* 由"行:列"反推偏移（原版 handleRowColChange）。
     原版把无效输入直接改写成 i18n 的「无效」两个字 —— 那会让用户没法接着改，
     这里改成**标红边框、保留输入**（见 MIGRATION.md）。 */
  function gotoFromRowCol() {
    var ta = findArea();
    if (!ta) return;
    var val = $('goto-rowcol').value;
    if (val.trim() === '') { gotoMark('rowcol', false); gotoMark('offset', false); gotoPos = null; return; }
    var m = /^\s*(\d+)\s*(?::\s*(\d+))?\s*$/.exec(val);
    if (!m) { gotoMark('rowcol', true); gotoPos = null; return; }
    var row = parseInt(m[1], 10);
    var col = m[2] === undefined ? 0 : parseInt(m[2], 10);
    var off = gotoOffsetOf(ta.value, row, col);
    if (off < 0) { gotoMark('rowcol', true); gotoPos = null; return; }
    gotoMark('rowcol', false);
    gotoMark('offset', false);
    $('goto-offset').value = String(off);
    gotoPos = { row: row, col: col };
  }

  /* 由偏移反推"行:列"（原版 handleOffsetChange）。 */
  function gotoFromOffset() {
    var ta = findArea();
    if (!ta) return;
    var val = $('goto-offset').value;
    if (val.trim() === '') { gotoMark('rowcol', false); gotoMark('offset', false); gotoPos = null; return; }
    if (!/^\s*\d+\s*$/.test(val)) { gotoMark('offset', true); gotoPos = null; return; }
    var off = parseInt(val, 10);
    if (off > ta.value.length) { gotoMark('offset', true); gotoPos = null; return; }
    gotoMark('rowcol', false);
    gotoMark('offset', false);
    var before = ta.value.slice(0, off);
    var nl = before.lastIndexOf('\n');
    $('goto-rowcol').value = (before.split('\n').length - 1) + ':' + (off - nl - 1);
    gotoPos = { row: before.split('\n').length - 1, col: off - nl - 1 };
  }

  function gotoBuild() {
    var ov = document.createElement('div');
    ov.className = 'overlay';
    ov.id = 'goto-overlay';
    ov.hidden = true;
    ov.innerHTML =
      '<div class="dialog dialog-narrow">' +
        '<div class="dialog-head"><span>跳转行&amp;列</span>' +
          '<button class="btn btn-icon" id="goto-x" title="关闭">✕</button></div>' +
        '<div class="dialog-body">' +
          '<label class="field"><span>行[:列]：</span>' +
            '<input type="text" id="goto-rowcol" spellcheck="false"></label>' +
          '<label class="field"><span>偏移：</span>' +
            '<input type="text" id="goto-offset" spellcheck="false"></label>' +
        '</div>' +
        '<div class="dialog-foot">' +
          '<button class="btn" id="goto-cancel">取消</button>' +
          '<button class="btn btn-primary" id="goto-ok">确定</button>' +
        '</div>' +
      '</div>';
    document.body.appendChild(ov);

    $('goto-rowcol').addEventListener('input', gotoFromRowCol);
    $('goto-offset').addEventListener('input', gotoFromOffset);
    $('goto-x').addEventListener('click', gotoClose);
    $('goto-cancel').addEventListener('click', gotoClose);
    $('goto-ok').addEventListener('click', gotoApply);
    ov.addEventListener('keydown', function (e) {
      if (e.key === 'Escape') { gotoClose(); e.preventDefault(); e.stopPropagation(); }
      else if (e.key === 'Enter') { gotoApply(); e.preventDefault(); e.stopPropagation(); }
    });
    return ov;
  }

  function gotoOpen() {
    var ta = findArea();
    if (!ta) return;
    var ov = $('goto-overlay') || gotoBuild();
    ov.hidden = false;
    gotoClearMark();
    gotoPos = null;
    // 打开时用当前 caret 位置填"偏移"，再反推出行:列（原版 setOnShown）
    $('goto-offset').value = String(ta.selectionStart);
    gotoFromOffset();
    $('goto-rowcol').focus();
    $('goto-rowcol').select();
  }

  function gotoClose() {
    var ov = $('goto-overlay');
    if (ov) ov.hidden = true;
    gotoPos = null;
    var ta = findArea();
    if (ta) ta.focus();
  }

  /* 确定：滚到那一行并移动 caret（原版 doGotoRowCol 的
     showParagraphAtCenter + moveTo）。 */
  function gotoApply() {
    var ta = findArea();
    var ov = $('goto-overlay');
    if (ov) ov.hidden = true;
    if (!ta || !gotoPos) return;
    var off = gotoOffsetOf(ta.value, gotoPos.row, gotoPos.col);
    if (off < 0) return;
    ta.setSelectionRange(off, off);
    findScrollTo(ta, off);
    ta.focus();
  }

  function initFindBar() {
    var win = $('content-win');
    var bar = document.createElement('div');
    bar.className = 'findbar';
    bar.id = 'content-find';
    bar.hidden = true;
    bar.innerHTML =
      '<input type="text" id="find-input" class="find-input" placeholder="搜索" spellcheck="false">' +
      '<span class="find-count" id="find-count">0 结果</span>' +
      '<button class="btn btn-icon" id="find-prev" title="上一个">↑</button>' +
      '<button class="btn btn-icon" id="find-next" title="下一个">↓</button>' +
      '<button class="btn btn-icon" id="find-x" title="关闭">✕</button>';
    win.insertBefore(bar, $('content-body'));

    $('find-input').addEventListener('input', function () { findUpdate(false); });
    $('find-prev').addEventListener('click', function () { findStep(-1); });
    $('find-next').addEventListener('click', function () { findStep(1); });
    $('find-x').addEventListener('click', findClose);

    /* Ctrl+F / Ctrl+G —— 原版挂在 CodeEditor 上，这里挂在面板上（焦点在
       面板内任意处都生效，与原版"焦点在代码区"基本等价）。预览模式也能搜了：
       findArea() 在预览下返回 hidden 的镜像 textarea（不再为 null），所以
       Ctrl+F 会真正打开查找条（见 renderContent 的镜像注入与 MIGRATION.md §五）。 */
    win.addEventListener('keydown', function (e) {
      if (!e.ctrlKey || e.altKey) return;
      if (e.key !== 'f' && e.key !== 'F' && e.key !== 'g' && e.key !== 'G') return;
      // 既没有编辑器 textarea、也没有预览镜像（加载/错误/hex/不支持态）时，
      // 别把按键吞掉，交给浏览器默认行为
      if (!findArea()) return;
      e.preventDefault();
      e.stopPropagation();
      if (e.key === 'f' || e.key === 'F') {
        if (findState.open) { $('find-input').focus(); $('find-input').select(); }
        else findOpen();
      } else {
        gotoOpen();
      }
    });

    // 查找框里的 Esc 关它（原版 CodeEditor.onKeyReleased 的 ESCAPE 分支）
    bar.addEventListener('keydown', function (e) {
      if (e.key === 'Escape') { findClose(); e.preventDefault(); e.stopPropagation(); }
    });
  }

  // ------------------------------------------------------------ 文本编辑

  function buildTextEditor(v, withCharset) {
    var box = document.createElement('div');
    box.className = 'editbox';

    var ta = document.createElement('textarea');
    ta.className = 'edit-text';
    ta.spellcheck = false;
    ta.value = v.text || '';
    ta.addEventListener('input', function () {
      v.text = ta.value;
      v.dirty = true;
      updateEditHint();
    });
    box.appendChild(ta);
    v.__ta = ta;

    // 源码是反编译出来的文本，原文件没有"字符编码"这回事（导出时一律 UTF-8），
    // 所以不给编码栏 —— 摆一个切了也没用的选择器只会让人以为哪里错了。
    if (withCharset) box.appendChild(buildCharsetBar(v));
    return box;
  }

  /* 编码选择。原文件是 GBK 的就必须仍按 GBK 存回去 —— 否则这个包从这一刻起
     整段变乱码。换编码要重新解码，所以是"重新拉一次后端"，与原版切编码时
     "重新 parse"一致；有未保存修改时先拦下来，不然改了半天的内容会静默消失。 */
  function buildCharsetBar(v) {
    var bar = document.createElement('div');
    bar.className = 'edit-bar';

    var lab = document.createElement('span');
    lab.className = 'edit-label';
    lab.textContent = '字符编码：';
    bar.appendChild(lab);

    var sel = document.createElement('select');
    sel.className = 'edit-charset';
    ['UTF-8', 'GBK'].forEach(function (cs) {
      var o = document.createElement('option');
      o.value = cs;
      o.textContent = cs;
      sel.appendChild(o);
    });
    sel.value = v.charset || 'UTF-8';
    sel.addEventListener('change', function () {
      if (v.dirty) {
        toast('有未保存的修改，请先保存或还原再切换编码', true);
        sel.value = v.charset || 'UTF-8';
        return;
      }
      var want = sel.value;
      return call('OpenEditor', { side: v.treeKey, path: v.path, charset: want })
        .then(function (nv) {
          if (!nv) return;
          adoptEditorView(nv, v.treeKey);
          renderEditor();
        })
        .catch(function (e) { toast(errText(e), true); });
    });
    bar.appendChild(sel);
    return bar;
  }

  // --------------------------------------------------------- 十六进制编辑

  function buildHexEditor(v) {
    var box = document.createElement('div');
    box.className = 'editbox hexbox';

    var grid = document.createElement('div');
    grid.className = 'hex-grid';
    box.appendChild(grid);
    v.__grid = grid;

    renderHexPage(v);

    // 分页：只渲染当前页的 DOM。整段铺出来的话，一个 1 MB 的文件就是
    // 6.5 万个输入框，直接卡死。
    if (v.bytes.length > HEX_PAGE) {
      var bar = document.createElement('div');
      bar.className = 'edit-bar';

      var prev = document.createElement('button');
      prev.type = 'button';
      prev.className = 'btn';
      prev.id = 'hex-prev';
      prev.textContent = '上一页';
      var next = document.createElement('button');
      next.type = 'button';
      next.className = 'btn';
      next.id = 'hex-next';
      next.textContent = '下一页';
      var info = document.createElement('span');
      info.className = 'edit-label';
      info.id = 'hex-info';

      bar.appendChild(prev);
      bar.appendChild(next);
      bar.appendChild(info);
      box.appendChild(bar);

      prev.addEventListener('click', function () {
        if (v.hexPage > 0) { v.hexPage--; renderHexPage(v); }
      });
      next.addEventListener('click', function () {
        if ((v.hexPage + 1) * HEX_PAGE < v.bytes.length) { v.hexPage++; renderHexPage(v); }
      });
    }

    return box;
  }

  function renderHexPage(v) {
    var grid = v.__grid;
    if (!grid) return;
    grid.textContent = '';

    var start = v.hexPage * HEX_PAGE;
    var end = Math.min(start + HEX_PAGE, v.bytes.length);

    for (var off = start; off < end; off += 16) {
      var line = document.createElement('div');
      line.className = 'hex-line';

      var addr = document.createElement('span');
      addr.className = 'hex-addr';
      addr.textContent = pad(off, 8);
      line.appendChild(addr);

      var hex = document.createElement('span');
      hex.className = 'hex-cells';
      var ascii = '';

      for (var i = 0; i < 16; i++) {
        var idx = off + i;
        if (idx < end) {
          var b = v.bytes[idx];
          var cell = document.createElement('input');
          cell.type = 'text';
          cell.className = 'hex-cell';
          cell.maxLength = 2;
          cell.value = pad(b.toString(16).toUpperCase(), 2);
          cell.dataset.off = String(idx);
          cell.addEventListener('input', onHexCell);
          hex.appendChild(cell);
          ascii += printable(b);
        } else {
          var blank = document.createElement('span');
          blank.className = 'hex-cell pad';
          hex.appendChild(blank);
        }
      }
      line.appendChild(hex);

      var asc = document.createElement('span');
      asc.className = 'hex-ascii';
      asc.textContent = ascii;
      line.appendChild(asc);

      grid.appendChild(line);
    }

    var info = $('hex-info');
    if (info) {
      var total = Math.ceil(v.bytes.length / HEX_PAGE) || 1;
      info.textContent = '第 ' + (v.hexPage + 1) + ' / ' + total
        + ' 页 · 共 ' + v.bytes.length + ' 字节';
    }
  }

  function onHexCell(e) {
    var v = contentWin;
    if (!v || !v.isEditor) return;

    var cell = e.target;
    var clean = cell.value.replace(/[^0-9a-fA-F]/g, '').slice(0, 2).toUpperCase();
    if (clean !== cell.value) cell.value = clean;
    if (!clean) return;   // 允许暂时空着（用户正在改），空值不写回

    var off = parseInt(cell.dataset.off, 10);
    v.bytes[off] = parseInt(clean, 16);
    v.dirty = true;
    updateEditHint();
    refreshAscii(cell);
  }

  /* ASCII 预览跟着改：不刷的话用户改完字节，右边那列还是旧字符，
     会以为没生效。 */
  function refreshAscii(cell) {
    var line = cell.closest ? cell.closest('.hex-line') : null;
    if (!line) return;
    var asc = line.querySelector('.hex-ascii');
    if (!asc) return;
    var cells = line.querySelectorAll('.hex-cell[data-off]');
    var s = '';
    for (var i = 0; i < cells.length; i++) {
      var b = parseInt(cells[i].value, 16);
      s += isNaN(b) ? '.' : printable(b);
    }
    asc.textContent = s;
  }

  function printable(b) {
    return (b >= 32 && b < 127) ? String.fromCharCode(b) : '.';
  }

  // ------------------------------------------------------------ 保存 / 还原

  function saveEditor(as) {
    var v = contentWin;
    if (!v || !v.isEditor) return Promise.resolve();
    if (v.mode === 'source' && !as) {
      // 后端也会拒（见 gui/editor.go 的 SaveContent），这里先拦一道：
      // 省一次往返，提示也能说得更具体。
      var tip = v.canCompile
        ? 'class 的源码不能直接存回包，请点「编译为 .class」或「另存为 .java」'
        : 'class 的源码不能直接存回包，请用「另存为 .java」';
      toast(tip, true);
      return Promise.resolve();
    }

    var opt = {
      side: v.treeKey, path: v.path, mode: v.mode,
      charset: v.charset || '', text: '', base64: ''
    };
    if (v.mode === 'hex') {
      opt.base64 = bytesToBase64(v.bytes);
    } else {
      opt.text = v.text || '';
    }

    busy(true, as ? '正在导出…' : '正在保存…');
    return call(as ? 'SaveContentAs' : 'SaveContent', opt)
      .then(function (s) {
        busy(false, '');
        if (s) apply(s);      // 节点上的"已修改"标记要跟着刷新
        if (as) {
          toast('已另存为文件');
        } else {
          v.dirty = false;
          updateEditHint();
          toast('已保存（打补丁 / 保存应用包时生效）');
        }
      })
      .catch(function (e) { busy(false, ''); toast(errText(e), true); });
  }

  function revertEditor() {
    var v = contentWin;
    if (!v || !v.isEditor) return Promise.resolve();
    // 当前是 hex 形态的话（可能来自「改字节」，也可能文件本来就是二进制），
    // 还原要停在同一个形态 —— 否则用户点「还原」会被弹回源码视图。
    return openEditor(v.treeKey, { path: v.path }, v.mode === 'hex');
  }

  /* setEditorError 把编译报错塞进编辑器里那条常驻错误条。
     javac 的多行诊断（"文件:行: 错误: …" + ^ 指示符 + "N 个错误"）塞进会自动
     消失的小 toast 根本看不清、也不能复制 —— 所以正文只走这里，toast 只作
     即时通知。传空串即清掉（编译成功或手动关闭时）。 */
  function setEditorError(msg) {
    var box = $('content-err');
    var body = $('content-err-body');
    if (!msg) {
      box.hidden = true;
      body.textContent = '';
      return;
    }
    body.textContent = msg;
    box.hidden = false;
  }

  function compileContent() {
    var v = contentWin;
    if (!v || !v.isEditor || v.mode !== 'source' || !v.canCompile) return Promise.resolve();
    if (!v.dirty) {
      toast('源码没有改动，无需编译', true);
      return Promise.resolve();
    }
    return confirmBox({
      title: '编译写回',
      text: '编译将用本机 JDK 把源码重新编回 .class，并写回当前节点。\n\n继续？',
      okText: '编译'
    }).then(function (ok) {
      if (!ok) return;
      busy(true, '正在编译…');
      return call('CompileContent', { side: v.treeKey, path: v.path, text: v.text || '' })
        .then(function (s) {
          busy(false, '');
          setEditorError(''); // 上次失败的报错，这次成功了就清掉
          if (s) apply(s);
          v.dirty = false;
          updateEditHint();
          toast('已编译写回（打补丁 / 保存应用包时生效）');
        })
        .catch(function (e) {
          busy(false, '');
          // 报错正文进常驻错误条（可滚动、可复制、不自动消失）；
          // toast 只作即时红条提醒，8 秒后自动消失不影响正文阅读。
          setEditorError(errText(e));
          toast('编译失败，错误已显示在下方', true);
        });
    });
  }

  function initEditorWin() {
    $('content-save').addEventListener('click', function () { saveEditor(false); });
    $('content-saveas').addEventListener('click', function () { saveEditor(true); });
    $('content-revert').addEventListener('click', function () { revertEditor(); });
    $('content-compile').addEventListener('click', function () { compileContent(); });
    $('content-err-x').addEventListener('click', function () { setEditorError(''); });
    $('content-bytes').addEventListener('click', function () {
      var v = contentWin;
      if (!v || !v.isEditor) return;
      if (v.dirty) { toast('有未保存的修改，请先另存为或还原再切换', true); return; }
      openEditor(v.treeKey, { path: v.path }, true);
    });
  }

  // ---------------------------------------------------------------- 小工具

  function pad(s, n) {
    s = String(s);
    while (s.length < n) s = '0' + s;
    return s;
  }

  function base64ToBytes(b64) {
    var bin = atob(b64 || '');
    var out = new Uint8Array(bin.length);
    for (var i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
    return out;
  }

  function bytesToBase64(bytes) {
    // 分块拼：一次性 String.fromCharCode.apply 传几十万个参数会爆栈
    var CHUNK = 0x8000;
    var parts = [];
    for (var i = 0; i < bytes.length; i += CHUNK) {
      parts.push(String.fromCharCode.apply(null, bytes.subarray(i, i + CHUNK)));
    }
    return btoa(parts.join(''));
  }

  /* 当前实际生效的编码。用户还没选过时是后端探测出来的那个。 */
  function paneCharset(index) {
    var v = contentWin;
    return index === 1 ? (v.rightCharset || '') : (v.leftCharset || '');
  }

  /* 右键菜单里的勾选态。
     这里刻意照搬原版的一个小毛病：ContentAreaFactory 创建菜单时无条件
     `charsetGroup.selectToggle(utf8Item)`，也就是**不管文件实际是什么编码，
     一律勾 UTF-8**。用户点过之后才跟着用户的选择走。 */
  function charsetChecked(index) {
    return (contentPicked[index] || 'UTF-8');
  }

  /* 内容预览的右键菜单。
   *
   * 逐条对齐原版 CodeAreaFactory.getContextMenu：
   *   复制 / 取消选择  —— 有选中文本才**显示**
   *   全选            —— 选中内容不等于全文才**显示**
   *   字符编码 > GBK / UTF-8（单选）
   *   差异模式        —— 只有可比对（有另一侧）时才出现
   *
   * 原版是 setVisible(false) 把它们藏起来；这里直接不渲染，
   * 视觉效果一样。 */
  function openContentMenu(x, y, index) {
    var v = contentWin;
    if (!v) return;

    var sel = window.getSelection ? String(window.getSelection()) : '';
    var full = paneText(index);
    var items = [];

    if (sel !== '') {
      items.push({ id: 'c-copy', label: '复制' });
      items.push({ id: 'c-deselect', label: '取消选择' });
    }
    if (sel !== full) items.push({ id: 'c-selectall', label: '全选' });

    items.push({
      id: 'c-charset', label: '字符编码', children: [
        { id: 'c-charset-gbk', label: 'GBK', radio: true, checked: charsetChecked(index) === 'GBK' },
        { id: 'c-charset-utf8', label: 'UTF-8', radio: true, checked: charsetChecked(index) === 'UTF-8' }
      ]
    });

    if (v.diffMode) {
      items.push({ id: 'c-diffmodel', label: '差异模式', radio: true, checked: !!v.diffModel });
    }

    openMenuItems(x, y, items, function (it) { onContentMenu(it, index, sel); });
  }

  function onContentMenu(it, index, sel) {
    switch (it.id) {
      case 'c-copy': {
        var ok = copyText(sel);
        return toast(ok ? '已复制' : '复制失败', !ok);
      }
      case 'c-deselect':
        if (window.getSelection) window.getSelection().removeAllRanges();
        return;
      case 'c-selectall':
        return selectPaneText(index);
      case 'c-charset-gbk':
        return reloadContent('GBK', index);
      case 'c-charset-utf8':
        return reloadContent('UTF-8', index);
      case 'c-diffmodel':
        return reloadContent(null, index, !contentWin.diffModel);
    }
  }

  /* 复制文本到剪贴板。
   *
   * 不用 navigator.clipboard：Wails 的 WebView 里它要求安全上下文与用户
   * 手势，可能被拒。走临时 textarea + execCommand('copy') 最稳，
   * 而且顺带解决了"点菜单会把选区弄丢"的问题 —— 文本是提前取好的。 */
  function copyText(text) {
    if (!text) return false;
    var ta = document.createElement('textarea');
    ta.value = text;
    ta.setAttribute('readonly', '');
    ta.style.position = 'fixed';
    ta.style.left = '-9999px';
    ta.style.top = '0';
    document.body.appendChild(ta);
    ta.select();
    var ok = false;
    try { ok = document.execCommand('copy'); } catch (e) { ok = false; }
    document.body.removeChild(ta);
    return ok;
  }

  /* 全选某一栏的代码文本。
     只框 .lt（正文），不含 .ln（行号）—— 对齐 JavaFX 的 codeArea.selectAll()。 */
  function selectPaneText(index) {
    var el = paneEl(index);
    if (!el) return;
    var lts = el.querySelectorAll('.lt');
    if (!lts.length) return;
    var range = document.createRange();
    range.setStartBefore(lts[0]);
    range.setEndAfter(lts[lts.length - 1]);
    var sel = window.getSelection();
    sel.removeAllRanges();
    sel.addRange(range);
  }

  /* 面板必须留在视口里的最小抓手宽度（像素）。
     为什么需要：原版那个 ContentView 是**系统窗口**（Dialog + 系统标题栏），
     拖到屏幕外还能靠系统窗口菜单 / 任务栏把它拉回来；这里是没有系统标题栏的
     应用内浮层，一旦标题栏离开视口就**再也没有抓手** —— 用户只能关掉重开，
     而位置又是被记住的，重开还是原地，等于卡死。
     所以自己保证：标题栏整条留在视口内（纵向）、横向至少留这么宽。 */
  var CONTENT_GRAB_MIN = 72;

  /* 标题栏高度。拿不到（面板还没布局）时退回 26px —— 与 .contentwin-head 的
     实际高度一致，宁可多留一点也不能少留。 */
  function contentHeadH() {
    var h = $('content-head');
    var v = h ? h.offsetHeight : 0;
    return v > 0 ? v : 26;
  }

  /* 把一组几何夹回可视区。见 CONTENT_GRAB_MIN 的说明。
     横向：至少留 CONTENT_GRAB_MIN 宽的面板在视口里（左右各一个边界）；
     纵向：标题栏整条必须可见，所以 top 夹在 [0, innerHeight - 标题栏高]。
     视口比标题栏还矮时（极端情况）退化成 top = 0，不产生负值。 */
  function clampContentGeom(g) {
    var headH = contentHeadH();
    var minLeft = CONTENT_GRAB_MIN - g.w;
    var maxLeft = window.innerWidth - CONTENT_GRAB_MIN;
    var maxTop = Math.max(0, window.innerHeight - headH);
    return {
      w: g.w, h: g.h,
      left: Math.round(Math.min(Math.max(g.left, minLeft), maxLeft)),
      top: Math.round(Math.min(Math.max(g.top, 0), maxTop))
    };
  }

  /* 把面板摆到给定几何。尺寸与位置都走内联样式 —— 最大化 / 还原只是换一组值。
     几何**统一在这里夹一次**：默认几何、记住的几何、最大化、还原、拖动、
     视口变化，全都汇到这一个出口，不会出现"某条路径忘了夹"的漏洞。 */
  function applyContentGeom(g) {
    var el = $('content-win');
    var c = clampContentGeom(g);
    el.style.width = c.w + 'px';
    el.style.height = c.h + 'px';
    el.style.left = c.left + 'px';
    el.style.top = c.top + 'px';
  }

  /* 首次打开、又没记住过尺寸时的默认几何。
     原版没有记忆尺寸时用主窗口尺寸，这里稍微收一点、留出边距。 */
  function defaultContentGeom() {
    var w = Math.min(920, Math.max(360, window.innerWidth - 120));
    var h = Math.min(640, Math.max(200, window.innerHeight - 180));
    return {
      w: w, h: h,
      left: Math.round((window.innerWidth - w) / 2),
      top: Math.max(56, Math.round((window.innerHeight - h) / 2))
    };
  }

  /* 最大化时的几何：铺满主窗口，只留 8px 边距（保留"这是浮窗"的观感）。
     与系统窗口最大化一致 —— 连工具栏一起盖住，回来靠标题栏上的还原按钮。 */
  function maxedContentGeom() {
    var m = 8;
    return {
      w: Math.max(360, window.innerWidth - m * 2),
      h: Math.max(200, window.innerHeight - m * 2),
      left: m, top: m
    };
  }

  function showContentWin() {
    var el = $('content-win');
    el.hidden = false;
    if (contentMaxed) {
      applyContentGeom(maxedContentGeom());
    } else if (contentSize) {
      applyContentGeom(contentSize);
    } else {
      applyContentGeom(defaultContentGeom());
    }
  }

  function hideContentWin() {
    // 编辑器模式下有未保存的改动：明确告诉用户被丢弃了。
    // 不拦着不让关 —— 原版连编辑功能都没有，这里选"提示但不阻塞"更好用。
    if (contentWin && contentWin.isEditor && contentWin.dirty) {
      toast('有未保存的修改，已丢弃', true);
    }
    rememberContentSize();
    $('content-win').hidden = true;
    $('content-foot').hidden = true;
    /* 最大化状态不跨"打开"保留 —— 原版每次预览都是 new Dialog()，
       新开一个就是普通尺寸。 */
    contentMaxed = false;
    contentRestore = null;
    $('content-win').classList.remove('maxed');
    syncMaxBtn();
    contentWin = null;
    hideMenu();
  }

  /* 记住面板的尺寸与位置。
     对应原版往 configuration 里存 contentWidth/contentHeight
     （那边只存尺寸，位置不存 —— 这里连位置一起记，浮窗体验更顺）。

     最大化时**不记**：否则铺满窗口的尺寸会把"普通尺寸"覆盖掉，
     下次打开就变成一个铺满窗口、却没处于最大化状态的怪东西。 */
  function rememberContentSize() {
    var el = $('content-win');
    if (!el || el.hidden) return;
    if (contentMaxed) return;
    if (!el.offsetWidth || !el.offsetHeight) return;
    contentSize = {
      w: el.offsetWidth, h: el.offsetHeight,
      left: el.offsetLeft, top: el.offsetTop
    };
  }

  /* 标题栏上那个最大化按钮的图标与提示语要跟着状态翻。
     图标用 SVG 写死在 innerHTML 里（不再用 textContent 设 "□"/"❐" 字符 ——
     字符会被 Segoe UI 渲染成尺寸不一的两个方块/交叉，且 textContent 会直接
     清掉 <button> 里的 <svg> 子节点）。两个态都用同 viewBox + 同 stroke 的
     SVG：普通态是单矩形（最大化），最大化态是两个错位的矩形（还原），
     画出来永远等大、和旁边的关闭叉叉也等大。 */
  var MAX_ICON = '<svg viewBox="0 0 12 12" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="2" y="2" width="8" height="8" rx="1"/></svg>';
  var RESTORE_ICON = '<svg viewBox="0 0 12 12" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="2" y="2" width="7" height="7" rx="1"/><rect x="3.5" y="3.5" width="7" height="7" rx="1"/></svg>';
  function syncMaxBtn() {
    var btn = $('content-max');
    if (!btn) return;
    btn.title = contentMaxed ? '还原' : '最大化';
    btn.innerHTML = contentMaxed ? RESTORE_ICON : MAX_ICON;
  }

  /* 最大化 / 还原。
     对应原版 ContentView 那个 setResizable(true) 的 Dialog —— JavaFX 会给
     可缩放的对话框挂上系统标题栏的最大化按钮。这里没有系统标题栏，就自己在
     标题栏上补一个，行为对齐：铺满可用区域、再点一次回到原来的尺寸与位置。 */
  function toggleContentMax() {
    var el = $('content-win');
    if (el.hidden) return;
    if (contentMaxed) {
      contentMaxed = false;
      var back = contentRestore || contentSize || defaultContentGeom();
      contentRestore = null;
      applyContentGeom(back);
    } else {
      contentRestore = {
        w: el.offsetWidth, h: el.offsetHeight,
        left: el.offsetLeft, top: el.offsetTop
      };
      contentMaxed = true;
      applyContentGeom(maxedContentGeom());
    }
    el.classList.toggle('maxed', contentMaxed);
    syncMaxBtn();
    // 面板尺寸变了，右键菜单的落点要重新算
    hideMenu();
  }

  function initContentWin() {
    var el = $('content-win');
    var head = $('content-head');

    $('content-x').addEventListener('click', hideContentWin);
    $('content-max').addEventListener('click', toggleContentMax);
    syncMaxBtn();

    /* 差异导航。按钮在标题栏里，但标题栏的拖动处理器会跳过 button
       （见下面的 mousedown），所以点按钮不会顺手把面板拖走。 */
    var navPrev = $('content-nav-prev'), navNext = $('content-nav-next');
    if (navPrev) navPrev.addEventListener('click', function () { contentNavGo(-1); });
    if (navNext) navNext.addEventListener('click', function () { contentNavGo(1); });

    // 拖动：按住标题栏移动。位置也一并记住。
    var drag = null;
    head.addEventListener('mousedown', function (e) {
      if (e.target.closest && e.target.closest('button')) return;
      // 最大化时标题栏拖不动 —— 与系统窗口一致
      if (contentMaxed) return;
      drag = {
        x: e.clientX, y: e.clientY, left: el.offsetLeft, top: el.offsetTop,
        w: el.offsetWidth, h: el.offsetHeight
      };
      e.preventDefault();
    });
    document.addEventListener('mousemove', function (e) {
      if (!drag) return;
      /* 走 applyContentGeom 而不是直接写 style —— 夹取在那一层，
         否则拖出视口就再也抓不回来了（见 CONTENT_GRAB_MIN）。 */
      applyContentGeom({
        w: drag.w, h: drag.h,
        left: drag.left + e.clientX - drag.x,
        top: drag.top + e.clientY - drag.y
      });
    });
    document.addEventListener('mouseup', function () {
      if (drag) { drag = null; rememberContentSize(); }
    });

    // 缩放：CSS 的 resize 手柄不会触发 mouseup 之外的事件，用 ResizeObserver 跟。
    if (window.ResizeObserver) {
      new ResizeObserver(function () { rememberContentSize(); refitGutters(); }).observe(el);
    }

    /* 视口变了：最大化时重新铺满；普通尺寸时**重新夹一次** ——
       主窗口被改小后，原来记得的位置可能已经在视口外了。 */
    window.addEventListener('resize', function () {
      if (el.hidden) return;
      if (contentMaxed) { applyContentGeom(maxedContentGeom()); return; }
      applyContentGeom({
        w: el.offsetWidth, h: el.offsetHeight,
        left: el.offsetLeft, top: el.offsetTop
      });
    });

    // 代码区滚动时菜单要收起来，否则会浮在原地对不上内容
    $('content-body').addEventListener('scroll', hideMenu, true);
  }

  // ---------------------------------------------------------------- 设置

  /* 上次应用过的「总是显示匹配连线」。用来判断这次点"应用"有没有改它 ——
     改了才需要去后端把配对数据重取一遍。 */
  var appliedLinks = null;

  // ------------------------------------------------------- 主题（明/暗）

  /* 对应原版 Configuration.Genera.Theme（ThemeEnum：LIGHT/DARK）与
     AppContext.updateTheme()：原版是往 Scene 上加/撤一张 theme-dark.css。
     Go 版换成改 <html data-theme>，让 app.css 里
     `:root[data-theme="oled"]` / `:root[data-theme="graphite"]`
     那一整块接管 —— 同一件事，但我们的暗色不是"再叠一张表"，而是换 token。

     与其它设置项的区别：**它不经后端**。Go 版 GUI 没有配置文件，设置项目
     前全是会话级的（见 ApplySettings 的说明）；主题比别的设置更值得记住，
     所以放 localStorage，重启后还在。

     取值（用户 2026-10-21 看过三套预览图后定下两套暗色）：
       'light'    明亮（默认）
       'graphite' 石墨 —— 中性深灰，层次柔和
       'oled'     纯黑 —— 对比最烈，OLED 屏不发光
     两套暗色在 app.css 里是并列的两块，差别只在变量值。
     ThemeEnum 只有 LIGHT/DARK 两个 code，'graphite'/'oled' 都算它的
     DARK；将来哪天接了配置文件要落盘，在这里折一下即可
     （theme === 'light' ? 'light' : 'dark'）。 */
  var THEME_KEY = 'patchassistant.theme';
  var THEMES = ['light', 'graphite', 'oled'];

  /* 读偏好。只认这三个值，别的（包括老的 'dark'）一律回落亮色 ——
     与其猜用户想表达哪套暗色，不如让他再选一次。 */
  function currentTheme() {
    try {
      var v = window.localStorage.getItem(THEME_KEY);
      return THEMES.indexOf(v) >= 0 ? v : 'light';
    } catch (e) {
      /* 禁用站点数据时读不到，按亮色 —— 反正亮色本来就是默认值。
         （引导脚本在 index.html 的 <head> 里，也做了同一层兜底。） */
      return 'light';
    }
  }

  /* 换主题。只做两件事：改 <html> 上的 data-theme（让 CSS 变量翻面），
     再把窗口底色同步过去。

     窗口底色为什么也要同步：WebView 只覆盖客户区，缩放边缘/圆角那一圈
     露出的是**窗口**底色（gui/main.go 的 BackgroundColour）。暗色下不换，
     窗口四周会留一圈浅色边。取 --bg 而不是写死色值，是为了和 CSS 只有
     一个出处 —— 换主题方案时不用记得再来改 Go。 */
  function applyTheme(theme) {
    if (theme === 'light') { document.documentElement.removeAttribute('data-theme'); }
    else { document.documentElement.setAttribute('data-theme', theme); }
    var bg = getComputedStyle(document.documentElement)
      .getPropertyValue('--bg').trim();
    if (bg) {
      call('SetWindowBackground', bg).catch(function () { /* 只是窗口边一圈，失败不打扰用户 */ });
    }
  }

  /* 设置弹窗里那个下拉。改完立刻生效 —— 主题是纯界面项，不像补丁配置
     那样要"重新匹配后才看得到"，所以没必要憋到「应用」按钮。 */
  function initTheme() {
    var sel = $('set-theme');
    if (!sel) return;
    var cur = currentTheme();
    applyTheme(cur);
    sel.value = cur;
    sel.addEventListener('change', function () {
      var v = THEMES.indexOf(this.value) >= 0 ? this.value : 'light';
      try { window.localStorage.setItem(THEME_KEY, v); } catch (e) { /* 存不住就当次会话有效 */ }
      applyTheme(v);
    });
  }

  function fillSettings(s) {
    $('set-with-path').checked = !!s.withPath;
    $('set-with-name').checked = !!s.withName;
    $('set-patch-file-diff').checked = !!s.patchFileDiff;
    $('set-write-sign').checked = !!s.writePatchSign;
    /* 历史条数 <= 0 时后端会回落到 30，这里只负责把值填出来，
       不在前端做夹取 —— 夹取口径只留后端一处，避免两边不一致。 */
    $('set-history-limit').value = s.historyLimit > 0 ? s.historyLimit : 30;
    $('set-regex').value = s.patchFileRegex || '';
    $('set-readme').value = s.readmeFile || '';
    $('set-user').value = s.username || '';
    $('set-show-links').checked = !!s.showMappedLinks;
    appliedLinks = !!s.showMappedLinks;
  }

  function readSettings() {
    return {
      withPath: $('set-with-path').checked,
      withName: $('set-with-name').checked,
      patchFileDiff: $('set-patch-file-diff').checked,
      writePatchSign: $('set-write-sign').checked,
      /* 输入框空串时 Number('') === 0，正好落进后端"<= 0 回落 30"那支。 */
      historyLimit: Number($('set-history-limit').value),
      patchFileRegex: $('set-regex').value,
      readmeFile: $('set-readme').value,
      username: $('set-user').value,
      showMappedLinks: $('set-show-links').checked
    };
  }

  function openSettings() {
    call('GetSettings').then(fillSettings).catch(function () {});
    $('settings-overlay').hidden = false;
  }

  function applySettings() {
    var wantLinks = $('set-show-links').checked;
    var linksChanged = appliedLinks !== null && appliedLinks !== wantLinks;
    call('ApplySettings', readSettings())
      .then(function (s) {
        fillSettings(s);
        $('settings-overlay').hidden = true;
        /* 连线开关是纯界面项、不需要重新匹配，所以改了它就把配对数据重取一遍，
           线立刻跟着变（关掉时后端就不下发 mappedLinks 了）。
           **只喂连线层、不走 apply()** —— apply 会重建两棵树，滚动位置跳回顶部，
           为了一个勾选框不值当。 */
        if (linksChanged) {
          call('GetState')
            .then(function (st) { linksSet(st && st.mappedLinks); })
            .catch(function () {});
        }
        toast('设置已应用（点"重新匹配"生效）');
      })
      .catch(function (e) { toast(String(e && e.message || e), true); });
  }

  /* -------------------------------------------------------- 分隔条拖动

     对应原版 HomeView 里两个 SplitPane 的 divider：
       #split-v：左右两棵树（topPane，水平方向）
       #split-h：树区与「补丁信息」（centrePane，垂直方向，原版默认 0.7）

     拖动直接改内联样式（左右用 flex-basis 百分比、下方用像素高度），
     不落盘 —— 原版 SplitPane 的 divider position 也只活在会话里。 */
  function initSplitters() {
    dragSplitter($('split-v'), function (e) {
      var panes = document.querySelector('.panes');
      var split = $('split-v');
      var r = panes.getBoundingClientRect();
      if (!r.width) return;
      /* 分隔条本身也是 flex 项、占着实打实的宽度，所以两棵树能分的只有
         "总宽 - 分隔条宽"。不扣掉它，拖到最右时右侧面板会被挤出容器。 */
      var splitW = split ? split.getBoundingClientRect().width : 0;
      var usable = Math.max(1, r.width - splitW);
      // 留边界余量，别把某一侧拖没
      var ratio = Math.min(0.9, Math.max(0.1, (e.clientX - r.left) / usable));
      var app = $('pane-app');
      var patch = $('pane-patch');
      var share = (usable / r.width) * 100;   // 两个面板加起来占总宽的百分比
      if (app) app.style.flex = '0 0 ' + (ratio * share).toFixed(2) + '%';
      if (patch) patch.style.flex = '0 0 ' + ((1 - ratio) * share).toFixed(2) + '%';
      // 面板宽度变了，连线的端点跟着走
      linksSchedule();
    });

    dragSplitter($('split-h'), function (e) {
      var info = document.querySelector('.info-pane');
      if (!info) return;
      /* 分隔条在状态栏之上：鼠标到 body 底部的距离，减去状态栏高度，
         就是「补丁信息」该有的高度。 */
      var bodyRect = document.body.getBoundingClientRect();
      var sb = document.querySelector('.statusbar');
      var sbH = sb ? sb.getBoundingClientRect().height : 0;
      var h = bodyRect.bottom - e.clientY - sbH;
      h = Math.min(Math.max(h, 60), bodyRect.height * 0.8);
      info.style.height = Math.round(h) + 'px';
      // 树区高度变了，连线要重算
      linksSchedule();
    });
  }

  /* 通用的"按住拖动"：mousedown 之后把 mousemove / mouseup 挂到 document 上，
     这样鼠标移出分隔条（甚至移出窗口）也不会丢事件。 */
  function dragSplitter(el, onMove) {
    if (!el) return;   // 走查 / 纯浏览器里可能没有这个元素
    el.addEventListener('mousedown', function (e) {
      e.preventDefault();
      el.classList.add('dragging');
      document.body.classList.add('resizing');
      function move(ev) { onMove(ev); }
      function up() {
        el.classList.remove('dragging');
        document.body.classList.remove('resizing');
        document.removeEventListener('mousemove', move);
        document.removeEventListener('mouseup', up);
      }
      document.addEventListener('mousemove', move);
      document.addEventListener('mouseup', up);
    });
  }

  // ------------------------------------------------------------ 包内容对比

  /* 对应「对比」弹窗（原版没有的入口，见 index.html 里的说明）。
     数据流：选两个包 → ComparePacks（Go 侧算好结构化结果 + 纯文本报告）
     → 汇总药膏 + 分组明细。拷贝 / 保存都是把 Go 算好的 r.text 原样传回，
     前端不重拼报告 —— 和「节点绑定信息」弹窗一个路子。 */
  var cmpState = { text: '', nameA: '', nameB: '' };

  /* 嵌套展开的导航栈：每帧 { chain, result }。chain 是从根包往里数的条目
     路径列表（空 = 顶层）。点嵌套 jar/zip 压栈、点「返回」弹栈；结果缓存在
     这里，返回时不必重跑对比（外层包可能上百 MB，重读代价高）。 */
  var cmpFrames = [];
  function cmpChain() {
    return cmpFrames.length ? cmpFrames[cmpFrames.length - 1].chain : [];
  }

  /* 弹窗尺寸状态（会话级，与其它设置项一致 —— GUI 没有配置文件）。
     cmpNormal 是"用户想要的普通尺寸"：拖过右下角之后浏览器会把宽高写成
     内联样式、一直挂在元素上，所以**关闭再打开本来就保留**；真正需要记住的
     只有"最大化时被清掉的那一份"—— 最大化会把内联宽高清空（否则内联优先级
     会顶住铺满规则），所以先把当时尺寸存进 cmpNormal，还原/重开时写回去。
     明细可能有几百行，固定 780×540 不够看，所以弹窗可拖右下角缩放、
     也可一键最大化（与内容预览面板同一套做法）。 */
  var cmpNormal = null;
  var cmpMaxed = false;
  var CMP_DEFAULT_GEOM = { w: 780, h: 540 };

  function cmpDialog() { return document.querySelector('.dialog-compare'); }
  function cmpHead() { return document.querySelector('.dialog-compare .dialog-head'); }

  /* 弹窗位置：按住标题栏拖动。
     与内容预览面板同一套手感，但定位方式不同 —— 面板是 fixed + left/top，
     而弹窗是被遮罩 flex **居中**的。这里用 transform 偏移（相对"居中位置"），
     好处是不用自己去算基准坐标、也不会跟 flex 居中打架。

     偏移量必须夹住（见 clampCompareOffset）：弹窗没有系统标题栏，
     一旦标题栏被拖出视口就再也没有抓手了 —— 只能关掉重开，
     而位置又是记住的，重开还在原地，等于卡死（与 CONTENT_GRAB_MIN 同一个教训）。 */
  var cmpOffset = { x: 0, y: 0 };
  var CMP_GRAB_MIN = 72;   // 至少留这么宽在视口里（与内容预览面板同一口径）

  /* 标题栏高度。拿不到（还没布局）时退回 26px —— 与 .dialog-head 的实际
     高度接近，宁可多留一点也不能少留。 */
  function cmpHeadH() {
    var h = cmpHead();
    var v = h ? h.offsetHeight : 0;
    return v > 0 ? v : 26;
  }

  /* 把偏移量夹回可视区。基准是"居中位置"：
       baseLeft = (视口宽 - 弹窗宽) / 2，baseTop 同理。
     要求：横向至少留 CMP_GRAB_MIN 宽；纵向整条标题栏可见
     （top ∈ [0, 视口高 - 标题栏高]）。 */
  function clampCompareOffset(o) {
    var dlg = cmpDialog();
    if (!dlg) return { x: 0, y: 0 };
    var w = dlg.offsetWidth;
    var h = dlg.offsetHeight;
    var baseLeft = (window.innerWidth - w) / 2;
    var baseTop = (window.innerHeight - h) / 2;
    var minX = CMP_GRAB_MIN - w - baseLeft;
    var maxX = Math.max(minX, window.innerWidth - CMP_GRAB_MIN - baseLeft);
    var minY = -baseTop;
    var maxY = Math.max(minY, window.innerHeight - cmpHeadH() - baseTop);
    return {
      x: Math.round(Math.min(Math.max(o.x, minX), maxX)),
      y: Math.round(Math.min(Math.max(o.y, minY), maxY))
    };
  }

  function applyCompareOffset() {
    var dlg = cmpDialog();
    if (!dlg) return;
    /* 最大化态要清掉偏移：内联 transform 会顶住 .maxed 的铺满规则
       （与"最大化要清内联宽高"是同一个道理）。 */
    if (cmpMaxed) { dlg.style.transform = ''; return; }
    cmpOffset = clampCompareOffset(cmpOffset);
    dlg.style.transform = (cmpOffset.x || cmpOffset.y)
      ? 'translate(' + cmpOffset.x + 'px,' + cmpOffset.y + 'px)'
      : '';
  }

  /* 普通尺寸写内联宽高 —— 原生缩放手柄（CSS resize: both）本来就是这么写的，
     这里只是把它记下来的值再写回去。 */
  function applyCompareGeom(g) {
    var dlg = cmpDialog();
    if (!dlg) return;
    dlg.style.width = Math.round(g.w) + 'px';
    dlg.style.height = Math.round(g.h) + 'px';
  }

  function syncCompareMaxBtn() {
    var btn = $('compare-max');
    if (!btn) return;
    btn.title = cmpMaxed ? '还原' : '最大化';
    btn.innerHTML = cmpMaxed ? RESTORE_ICON : MAX_ICON;
  }

  /* 最大化 / 还原。最大化时**清掉内联宽高**、改挂 .maxed —— 内联样式优先级
     高于样式表，不清掉的话用户拖出来的尺寸会顶住铺满规则（见 app.css）。 */
  function toggleCompareMax() {
    var dlg = cmpDialog();
    var ov = $('compare-overlay');
    if (!dlg || !ov || ov.hidden) return;
    if (cmpMaxed) {
      cmpMaxed = false;
      dlg.classList.remove('maxed');
      applyCompareGeom(cmpNormal || CMP_DEFAULT_GEOM);
    } else {
      cmpNormal = { w: dlg.offsetWidth, h: dlg.offsetHeight };
      cmpMaxed = true;
      dlg.style.width = '';
      dlg.style.height = '';
      dlg.classList.add('maxed');
    }
    // 位置也跟着状态走：最大化清偏移、还原写回去（见 applyCompareOffset）
    applyCompareOffset();
    syncCompareMaxBtn();
  }

  /* 取元素都带判空：全局 Esc 处理器会调 compareClose()，万一 HTML 与
     app.js 版本不一致（比如走查用的镜像 index.html 没同步），抛错会把
     整个 Esc 处理器打断，连累"预览面板 Esc 关闭"这类无关功能 ——
     实测踩过。initSearch / dragSplitter 也是同样的防护写法。 */
  function compareOpen() {
    var ov = $('compare-overlay');
    if (!ov) return;
    /* 最大化状态不跨"打开"保留 —— 与内容预览面板一致（原版每次预览都是
       new Dialog()，新开一个就是普通尺寸）；普通尺寸沿用用户调过的那个。 */
    cmpMaxed = false;
    var dlg = cmpDialog();
    if (dlg) {
      dlg.classList.remove('maxed');
      if (cmpNormal) applyCompareGeom(cmpNormal);
    }
    syncCompareMaxBtn();
    ov.hidden = false;
    // 位置要等元素真的显示出来再算 —— 隐藏时 offsetWidth 是 0，
    // 夹取会拿它当基准（算出来的偏移是错的）
    applyCompareOffset();
  }

  function compareClose() {
    var ov = $('compare-overlay');
    if (ov) ov.hidden = true;
  }

  function comparePick(role) {
    call('PickCompareJar', role)
      .then(function (p) {
        if (p) $(role === 'a' ? 'cmp-path-a' : 'cmp-path-b').value = p;
      })
      .catch(function (e) { toast(errText(e), true); });
  }

  /* 两个都是包（.jar/.zip）走包对比（ComparePacks）；两个都是普通文件走文件
     对比（CompareFiles）。大小写不敏感 —— 与文件对话框过滤器在 Windows 上
     的松口径一致（见 gui/jarcompare.go 的 PickCompareJar）。 */
  function isArchivePath(p) {
    var l = (p || '').toLowerCase().trim();
    return l.endsWith('.jar') || l.endsWith('.zip');
  }

  function compareRun() {
    var a = $('cmp-path-a').value.trim();
    var b = $('cmp-path-b').value.trim();
    if (!a || !b) {
      toast('请先选择两个要比对的对象', true);
      return;
    }
    /* 一个包一个文件没有统一的比法：包比的是"里面各条目"，文件比的是
       "整份内容"，混着比无从下手 —— 明确挡掉，别让后端给出误导性结果。 */
    var aArc = isArchivePath(a), bArc = isArchivePath(b);
    if (aArc !== bArc) {
      toast('一个是包、一个是普通文件，无法对比（要么两个包、要么两个文件）', true);
      return;
    }
    busy(true, '正在对比…');
    return call(aArc ? 'ComparePacks' : 'CompareFiles', a, b)
      .then(function (r) {
        busy(false, '');
        /* 新一轮对比：导航栈重置。包对比把顶层结果作为第 0 帧（供「返回」用），
           文件对比没有可展开的层级，栈清空即可。 */
        cmpFrames = (r && r.kind === 'pack') ? [{ chain: [], result: r }] : [];
        renderCompare(r);
        updateCmpBreadcrumb();
      })
      .catch(function (e) { busy(false, ''); toast(errText(e), true); });
  }

  function cmpPill(label, n, kind) {
    var s = document.createElement('span');
    s.className = 'cmp-pill cmp-pill-' + kind;
    s.textContent = label + ' ' + n;
    return s;
  }

  /* 一侧的展示。e 为 null 表示这个包没有该文件（新增/删除的另一边）。
     md5 只显示前 12 位，完整的在 title 与报告文本里 —— 32 位全显示
     两栏并排太挤，而校验用不了那么长的肉眼对比。 */
  function cmpSide(tag, e) {
    var s = document.createElement('span');
    s.className = 'cmp-side';
    if (!e) {
      s.classList.add('cmp-side-none');
      s.textContent = tag + ' —';
      return s;
    }
    var size = document.createElement('b');
    size.textContent = humanSize(e.size);
    var md = document.createElement('i');
    md.textContent = 'md5 ' + e.md5.slice(0, 12);
    md.title = 'md5 ' + e.md5 + '（' + e.size + ' 字节）';
    s.appendChild(document.createTextNode(tag + ' '));
    s.appendChild(size);
    s.appendChild(md);
    return s;
  }

  /* 行：路径一行 + A/B 两侧一行。三个分组（内容不同 / 仅 A 有 / 仅 B 有）
     共用这一种行型 —— 缺失的那侧显示 "—"，分组只换标题与颜色。

     整行可点：点开用内容预览浮窗看这个文件在两侧的内容（见 compareOpenFile）。
     三种分组都能点 —— "仅 A 有 / 仅 B 有"点开是单栏，一样有用（能看清那
     文件里到底是什么）。 */
  function cmpRow(c) {
    var row = document.createElement('div');
    row.className = 'cmp-row cmp-row-click';
    /* 嵌套 jar/zip：点它展开**里面的**逐条目对比（见 compareDrill）；
       普通文件：点它用内容浮窗看两侧内容（见 compareOpenFile）。 */
    var nested = isArchivePath(c.path);
    row.title = (nested ? '单击展开对比：' : '单击查看内容：') + c.path;
    row.addEventListener('click', function () {
      if (nested) { compareDrill(c.path); } else { compareOpenFile(c.path); }
    });
    var p = document.createElement('div');
    p.className = 'cmp-row-path';
    p.textContent = c.path;
    p.title = c.path;
    row.appendChild(p);
    var sides = document.createElement('div');
    sides.className = 'cmp-row-sides';
    sides.appendChild(cmpSide('A', c.a));
    sides.appendChild(cmpSide('B', c.b));
    row.appendChild(sides);
    return row;
  }

  function cmpGroup(title, n, kind, changes) {
    var g = document.createElement('div');
    g.className = 'cmp-group cmp-group-' + kind;
    var h = document.createElement('div');
    h.className = 'cmp-group-title';
    h.textContent = title + '（' + n + '）';
    g.appendChild(h);
    changes.forEach(function (c) { g.appendChild(cmpRow(c)); });
    return g;
  }

  /* 工具自己写进 META-INF/patch-up/ 下的 *.sign 是「补丁签名」
     （见 internal/service/jarcompare.go 的注释：每做一次打补丁都会
     重新写一遍），两次打补丁前后的两个 jar 在这里会刷出大量
     "仅 A 有 / 仅 B 有"的 .sign 条目，把真正想看的差异
     （业务代码、配置）淹没。
     口径（已与 MIGRATION.md 第 47 项登记）：
       - 后端 ComparePacks **不过滤** .sign —— 报告要诚实；
       - 这里按"界面隐藏、报告保留"：只把列表里的 .sign 藏起来，
         汇总药膏与分组标题的数跟着减，否则"仅 A 有 3"但列表只
         1 行会让人更困惑；
       - r.text 仍原样保留（拷贝报告/保存报告拿它拼），并在界面上
         写一条小提示告诉用户「已隐藏 N 个，看报告」。
     只认本工具的产物：jar 自带的 jar 签名（jarsigner 写的
     META-INF/*.SF / *.RSA / *.DSA / *.EC）不在这个范围内。
     路径是 zip 条目名原样（'/' 分隔），所以用 '/'. */
  function isCompareSignature(p) {
    return /^META-INF\/patch-up\/[^/]+\.sign$/i.test(p);
  }

  function renderCompare(r) {
    /* 结果有两种形态（后端用 kind 区分）：包对比（pack，条目列表）与
       文件对比（file，两个单文件）。走各自的分支。 */
    if (r && r.kind === 'file') { renderCompareFile(r); return; }

    var host = $('cmp-result');
    host.textContent = '';
    /* 报告文本整段原样：后端 ComparePacks 不过滤 .sign，r.text 里
       仍包含所有签名条目，"拷贝报告/保存报告"出去的也是这一份。 */
    cmpState.text = r.text || '';
    cmpState.nameA = r.nameA || '';
    cmpState.nameB = r.nameB || '';
    host.hidden = false;

    /* 三组先按路径过滤掉 .sign，sigN 累计被藏的条数（用于提示语）。
       r.changed / r.removed / r.added 本身不动 —— 后端给的形态完整保留。 */
    var sigN = 0;
    function take(arr) {
      var out = [];
      for (var i = 0; i < arr.length; i++) {
        if (isCompareSignature(arr[i].path)) { sigN++; continue; }
        out.push(arr[i]);
      }
      return out;
    }
    var changed = take(r.changed);
    var removed = take(r.removed);
    var added = take(r.added);

    var sum = document.createElement('div');
    sum.className = 'cmp-summary';
    /* 药膏用过滤后的长度：列表里看不到的就不该在这里计数。 */
    sum.appendChild(cmpPill('内容不同', changed.length, 'mod'));
    sum.appendChild(cmpPill('仅 A 有', removed.length, 'del'));
    sum.appendChild(cmpPill('仅 B 有', added.length, 'add'));
    sum.appendChild(cmpPill('相同', r.same, 'same'));
    host.appendChild(sum);

    var names = document.createElement('div');
    names.className = 'cmp-names';
    names.textContent = 'A：' + r.nameA + '（' + r.totalA + ' 个文件）　B：' +
      r.nameB + '（' + r.totalB + ' 个文件）';
    host.appendChild(names);

    if (sigN > 0) {
      /* 透明性：藏了东西要让用户知道去哪儿找。一行小字、不抢主表风头。 */
      var note = document.createElement('div');
      note.className = 'cmp-filtered-note';
      note.textContent = '已隐藏 ' + sigN + ' 个 META-INF/patch-up/*.sign 签名条目'
        + '（详见「拷贝报告 / 保存报告」）';
      host.appendChild(note);
    }

    var diffN = changed.length + removed.length + added.length;
    if (diffN === 0) {
      var eq = document.createElement('div');
      eq.className = 'cmp-empty';
      eq.textContent = '两个包的文件内容完全一致（' + r.same + ' 个文件）。';
      host.appendChild(eq);
    } else {
      // 行可点这件事得说出来 —— 不写用户不会去点（写了才知道能看内容）
      var tip = document.createElement('div');
      tip.className = 'cmp-hint';
      tip.textContent = '单击下面任意一行：普通文件看两侧内容（.class 会先反编译，稍等一秒），'
        + 'jar / zip 包可继续展开看里面的差异';
      host.appendChild(tip);

      host.appendChild(cmpGroup('内容不同', changed.length, 'mod', changed));
      host.appendChild(cmpGroup('仅 A 有', removed.length, 'del',
        removed.map(function (e) { return { path: e.path, a: e, b: null }; })));
      host.appendChild(cmpGroup('仅 B 有', added.length, 'add',
        added.map(function (e) { return { path: e.path, a: null, b: e }; })));
    }

    $('cmp-copy').disabled = false;
    $('cmp-save').disabled = false;
  }

  /* 文件对比结果（r.kind === 'file'）。
     两个普通文件没有"条目列表"可言，所以形态比包对比简单：一行汇总
     （相同/不同）+ 两侧文件名/大小/md5。不同时给一个「查看差异」按钮，
     点开用内容预览浮窗并排看两个文件 —— 复用与包对比同一套 diff 渲染，
     不另造一份。报告文本 r.text 仍由 Go 算好（拷贝/保存拿它）。 */
  function renderCompareFile(r) {
    var host = $('cmp-result');
    host.textContent = '';
    cmpState.text = r.text || '';
    cmpState.nameA = r.nameA || '';
    cmpState.nameB = r.nameB || '';
    /* 存两侧路径：面板里切编码 / 切差异模式时要回这两份文件取数
       （见 compareOpenFiles 与 reloadContent 的 fromFile 分支）。 */
    cmpState.left = r.pathA || '';
    cmpState.right = r.pathB || '';
    host.hidden = false;

    var sum = document.createElement('div');
    sum.className = 'cmp-summary';
    sum.appendChild(cmpPill(r.same ? '相同' : '不同', 1, r.same ? 'same' : 'mod'));
    host.appendChild(sum);

    var names = document.createElement('div');
    names.className = 'cmp-names';
    names.textContent = 'A：' + r.nameA + '（' + humanSize(r.sizeA) + '，md5 '
      + String(r.md5A).slice(0, 12) + '）　B：' + r.nameB + '（' + humanSize(r.sizeB)
      + '，md5 ' + String(r.md5B).slice(0, 12) + '）';
    host.appendChild(names);

    if (r.same) {
      var eq = document.createElement('div');
      eq.className = 'cmp-empty';
      eq.textContent = '两个文件内容完全一致。';
      host.appendChild(eq);
    } else {
      var tip = document.createElement('div');
      tip.className = 'cmp-hint';
      tip.textContent = '两个文件内容不同 —— 点「查看差异」用内容预览浮窗并排看'
        + '（.class 会先反编译，稍等一秒）';
      host.appendChild(tip);

      var row = document.createElement('div');
      row.className = 'cmp-file-diff-row';
      var btn = document.createElement('button');
      btn.className = 'btn btn-primary';
      btn.textContent = '查看差异';
      btn.addEventListener('click', compareOpenFiles);
      row.appendChild(btn);
      host.appendChild(row);
    }

    $('cmp-copy').disabled = false;
    $('cmp-save').disabled = false;
  }

  /* 打开两个文件的差异预览（对应文件对比结果里的「查看差异」按钮）。
     与包对比的 compareOpenFile 同一套路：先把面板摆出来（.class 慢，
     显示"正在反编译…"），再取数重绘。来源是磁盘上的两个文件，
     走 OpenFileCompare；fromFile 让面板里的编码/差异模式切换知道回哪取数。

     **两个路径必须存在 fileLeft / fileRight 里，不能图省事写 left / right**：
     后端 ContentView 把两栏序列化成 json:"left" / json:"right"（值是
     `{path, lines}` 对象）。这里若写 `v.left = left`，就用一个路径字符串把那个
     对象整个盖掉 —— makePane 里 `pane.lines` 变成 undefined，两栏全空白，
     而标题和 blocks 不受影响，于是出现"标题在、差异计数在、就是没内容"。
     真实踩过：用户报"点查看差异进去是空白的"。 */
  function compareOpenFiles() {
    var left = cmpState.left, right = cmpState.right;
    if (!left || !right) return;
    var slow = /\.class$/i.test(left);
    if (slow) {
      contentPicked = {};
      contentWin = {
        mode: 'loading',
        title: (cmpState.nameA || left) + ' ↔ ' + (cmpState.nameB || right),
        message: '正在反编译…', fromFile: true, fileLeft: left, fileRight: right
      };
      renderContent();
      showContentWin();
    }
    return call('OpenFileCompare', { left: left, right: right })
      .then(function (v) {
        if (!v) return;
        if (v.mode === 'unsupported') {
          hideContentWin();
          toast(v.message || '该类型暂不支持预览', true);
          return;
        }
        contentPicked = {};
        v.fromFile = true;
        v.fileLeft = left;
        v.fileRight = right;
        contentWin = v;
        renderContent();
        showContentWin();
      })
      .catch(function (e) {
        if (slow) hideContentWin();
        toast(errText(e), true);
      });
  }

  /* 展开嵌套 jar/zip：把 A/B 两个包里**同一路径链**的嵌套包读出来再比一次。
     路径链由当前帧的 chain 追加本条目得到（见 cmpChain），结果压进导航栈；
     返回时弹栈（结果已缓存，不必重跑）。 */
  function compareDrill(entryPath) {
    var chain = cmpChain().concat([entryPath]);
    busy(true, '正在展开…');
    return call('OpenNestedCompare', chain)
      .then(function (r) {
        busy(false, '');
        if (!r) return;
        cmpFrames.push({ chain: chain, result: r });
        renderCompare(r);
        updateCmpBreadcrumb();
      })
      .catch(function (e) { busy(false, ''); toast(errText(e), true); });
  }

  /* 返回上一层：弹掉当前帧，重绘上一帧缓存的结果。顶层（只剩第 0 帧）时无效。 */
  function compareBack() {
    if (cmpFrames.length <= 1) return;
    cmpFrames.pop();
    renderCompare(cmpFrames[cmpFrames.length - 1].result);
    updateCmpBreadcrumb();
  }

  /* 面包屑：非顶层时显示当前嵌套路径 + 「← 返回」。
     路径可能很长（外层包名 + 逐层条目），靠 CSS 省略号 + title 看全。 */
  function updateCmpBreadcrumb() {
    var host = $('cmp-crumb');
    if (!host) return;
    var chain = cmpChain();
    host.textContent = '';
    if (!chain.length) { host.hidden = true; return; }
    host.hidden = false;
    var back = document.createElement('button');
    back.className = 'btn cmp-crumb-back';
    back.textContent = '← 返回';
    back.addEventListener('click', compareBack);
    host.appendChild(back);
    var label = document.createElement('span');
    label.className = 'cmp-crumb-path';
    label.textContent = chain.join(' / ');
    label.title = label.textContent;
    host.appendChild(label);
  }

  /* 拖到弹窗上：落点坐标 -> 槽号（'a' / 'b'），不在弹窗里返回 null。
     与 paneAt 同一套思路 —— 用浏览器自己的命中测试，别把布局换算搬到后端。
     槽上挂的是 data-slot（见 index.html），所以要往上找到带它的祖先。 */
  function compareDropTarget(x, y) {
    var el = document.elementFromPoint(x, y);
    while (el) {
      if (el.dataset && el.dataset.slot) return el.dataset.slot;
      el = el.parentElement;
    }
    return null;
  }

  /* 拖进弹窗后的填槽。收不收、哪个是 A 哪个是 B 由 Go 侧的
     HandleCompareDrop 判（规则可单测），这里只管"填到哪个槽"：
       - 一次拖两个包：按 A、B 顺序填，不看落点（最自然的用法）；
       - 拖一个：拖在哪个槽上就填哪个槽。 */
  function compareDropFiles(slot, paths) {
    call('HandleCompareDrop', paths)
      .then(function (r) {
        if (r.b) {
          $('cmp-path-a').value = r.a;
          $('cmp-path-b').value = r.b;
        } else {
          $(slot === 'b' ? 'cmp-path-b' : 'cmp-path-a').value = r.a;
        }
      })
      .catch(function (e) { toast(errText(e), true); });
  }

  /* 点结果里的一行 -> 用内容预览浮窗看这个文件在 A / B 两侧的内容。
     浮窗的 z-index(65) 本来就高于遮罩(50)，所以它盖在对比弹窗上面 ——
     不用关掉对比弹窗就能连着翻看多个文件，关掉浮窗就回到列表。

     .class 要先起 JVM 反编译（本机实测约 1 秒），与树预览一样**先**把面板
     摆出来显示"正在反编译…"，别让用户对着没反应的界面干等。 */
  function compareOpenFile(entryPath) {
    /* 当前嵌套上下文：展开过嵌套 jar 后，条目在**内层包**里 —— 得把这条
       路径链一起传给后端，它才知道从哪一层读（见 readContextEntry）。 */
    var chain = cmpChain();
    var slow = /\.class$/i.test(entryPath);
    if (slow) {
      contentPicked = {};
      contentWin = {
        mode: 'loading', title: entryPath, message: '正在反编译…',
        fromCompare: true, path: entryPath, chain: chain
      };
      renderContent();
      showContentWin();
    }
    /* chain 为空（顶层）时不带这个字段 —— 与后端 omitempty 一致，也免得
       走查里那条"只发条目路径"的断言因为多一个空字段而对不上。 */
    var opt = { path: entryPath };
    if (chain.length) opt.chain = chain;
    return call('OpenCompareContent', opt)
      .then(function (v) {
        if (!v) return;
        /* 没有解析器的类型（图片…）：Go 侧给了一句带两侧大小的说明。
           这里**无条件**收起浮窗（树预览那条路只在 slow 时收）—— 对比场景下
           用户是在列表里一个个点着看，浮窗里若留着上一个文件的内容，
           看起来就像"点开的其实是它"。宁可空着也不要给错的东西。 */
        if (v.mode === 'unsupported') {
          hideContentWin();
          toast(v.message || '该类型暂不支持预览', true);
          return;
        }
        contentPicked = {};
        // fromCompare 让面板里的编码切换 / 差异模式切换知道该回哪边取数
        // （见 reloadContent 的分流）。side 在这里没有意义。
        v.fromCompare = true;
        v.path = entryPath;
        v.chain = chain;
        contentWin = v;
        renderContent();
        showContentWin();
      })
      .catch(function (e) {
        if (slow) hideContentWin();
        toast(errText(e), true);
      });
  }

  function initCompare() {
    var ov = $('compare-overlay');
    if (!ov) return;   // HTML 与 app.js 版本不一致时别把 init() 带崩
    $('btn-compare').addEventListener('click', compareOpen);
    $('compare-x').addEventListener('click', compareClose);
    $('compare-max').addEventListener('click', toggleCompareMax);
    syncCompareMaxBtn();
    $('cmp-close').addEventListener('click', compareClose);
    /* 点遮罩不关 —— 对比窗口里点一下空白就把整窗关掉太容易误伤（见 init 里确认框那段）。 */
    $('cmp-pick-a').addEventListener('click', function () { comparePick('a'); });
    $('cmp-pick-b').addEventListener('click', function () { comparePick('b'); });
    $('cmp-run').addEventListener('click', compareRun);

    /* 拖动：按住标题栏移动（见 cmpOffset 那段说明）。
       mousedown 记起点，mousemove / mouseup 挂到 document 上 —— 鼠标移出
       弹窗（甚至移出窗口）也不丢事件，与 dragSplitter 是同一套写法。 */
    var head = cmpHead();
    var drag = null;
    if (head) {
      head.addEventListener('mousedown', function (e) {
        // 标题栏上的按钮是"点"不是"拖"：别把最大化 / 关闭也一起拖走
        if (e.target.closest && e.target.closest('button')) return;
        if (cmpMaxed) return;   // 最大化时拖不动 —— 与系统窗口一致
        drag = { x: e.clientX, y: e.clientY, ox: cmpOffset.x, oy: cmpOffset.y };
        e.preventDefault();
      });
      document.addEventListener('mousemove', function (e) {
        if (!drag) return;
        cmpOffset = {
          x: drag.ox + e.clientX - drag.x,
          y: drag.oy + e.clientY - drag.y
        };
        applyCompareOffset();
      });
      document.addEventListener('mouseup', function () { drag = null; });

      /* 视口变小后重新夹一次 —— 否则原本贴边的偏移会把弹窗顶到视口外，
         而偏移是记住的，重开还在原地（同内容预览面板的处理）。 */
      window.addEventListener('resize', function () {
        if (ov.hidden) return;
        applyCompareOffset();
      });
    }

    // 拷贝：对齐「节点绑定信息」弹窗的做法 —— 拷完按钮短暂变成"已拷贝！！"并置灰
    $('cmp-copy').addEventListener('click', function () {
      var btn = $('cmp-copy');
      call('CopyCompareReport', cmpState.text)
        .then(function () {
          btn.disabled = true;
          btn.textContent = '已拷贝！！';
          setTimeout(function () {
            btn.disabled = false;
            btn.textContent = '拷贝报告';
          }, 3000);
        })
        .catch(function (e) { toast(errText(e), true); });
    });
    // 保存：默认文件名由 Go 侧按两个包名拼（见 compareReportDefaultName）。
    // 不用 runVoid —— 用户在对话框里取消时 Go 返回 false，得区分"已保存"
    // 与"已取消"，不能一律报已保存。
    $('cmp-save').addEventListener('click', function () {
      call('SaveCompareReport', cmpState.nameA, cmpState.nameB, cmpState.text)
        .then(function (saved) { toast(saved ? '已保存' : '已取消保存'); })
        .catch(function (e) { toast(errText(e), true); });
    });
  }

  // ---------------------------------------------------------------- 绑定

  function init() {
    /* 主题要第一个初始化：它决定整个界面所有颜色的取值，放在后头意味着
       别的模块初始化时看到的还是上一套颜色。data-theme 属性本身已由
       index.html <head> 里的引导脚本设好（避免首帧闪白），这里补的是
       给下拉框填当前值、挂 change，以及把窗口底色同步过去。 */
    initTheme();
    $('btn-open-app').addEventListener('click', function () { pickAndOpen('app'); });
    $('btn-open-patch').addEventListener('click', function () { pickAndOpen('patch'); });
    $('btn-refresh').addEventListener('click', reMatch);
    $('btn-collapse-all').addEventListener('click', collapseAll);
    // 「单包视图」↔「双栏视图」：隐藏补丁面板 / 分隔条 / 补丁信息，只留应用包
    // 这侧占满整窗，方便把单个文件拖到具体应用文件上做"单文件替换"。再点一次还原。
    //
    // **按钮文案跟着状态走，而且写的是"点一下会切到哪个视图"**：
    //   双栏时 → 「单包视图」（点了就只剩应用包这一侧）
    //   单包时 → 「双栏视图」（点了就回到左右两栏）
    // 早先文案写死成「单包视图」，切过去之后按钮还写着「单包视图」——
    // 用户 2026-10-22 报障："在双栏下，点击单包视图，会只显示 jar 页，但是上面
    // 还是单包视图"。状态变了而按钮不变，等于唯一的状态提示是假的：
    // 布局确实变了，但看不出"现在能不能点回去、点了回去长什么样"。
    // 配套还有 .btn.active（见 app.css）—— 那个 class 以前也在加，
    // 但**没有任何规则命中它**，所以按钮在单包视图下和静止态一模一样。
    var singleViewBtn = $('btn-single-view');
    function syncSingleViewBtn() {
      var on = document.body.classList.contains('single-app');
      singleViewBtn.textContent = on ? '双栏视图' : '单包视图';
      singleViewBtn.classList.toggle('active', on);
      singleViewBtn.setAttribute('aria-pressed', on ? 'true' : 'false');
      singleViewBtn.title = on
        ? '恢复左右双栏（应用包 + 补丁包并排）'
        : '只显示应用包这一侧，方便把单个文件拖到具体文件上替换内容';
    }
    singleViewBtn.addEventListener('click', function () {
      document.body.classList.toggle('single-app');
      syncSingleViewBtn();
      linksSchedule();
    });
    syncSingleViewBtn();
    // 「临时补丁」面板头的全部撤销（与右键「恢复原状」同一个动作）
    $('temp-patch-clear').addEventListener('click', function () { resetManualDel(); });
    // 确认框只有三个出口：确定 / 取消 / Esc（下面那个全局 keydown 里）。
    /* **点遮罩不关，全界面的弹窗都按这条来** ——
       在窗口里随便点一下就把正在看的对话框（尤其是"要不要删"这种）关掉，
       比"必须点取消"更烦，而且确认框被误关等于默默取消了操作。
       所以这里不挂遮罩点击，其它弹窗（设置/签名/绑定信息/搜索/对比）同样不挂。 */
    $('confirm-ok').addEventListener('click', function () { if (confirmPending) confirmPending(true); });
    $('confirm-cancel').addEventListener('click', function () { if (confirmPending) confirmPending(false); });
    /* 「拖入待替换」清单：点行只定位，替换必须再点一次 —— 两步走，不误改。 */
    $('pending-cancel').addEventListener('click', function () { clearPending(); });
    $('pending-ok').addEventListener('click', pendingApply);
    /* 按路径匹配：名字反查不到时的第二条路。回车等同点「定位」——
       粘完路径手还在键盘上，不该逼人去点按钮。 */
    $('pending-path-go').addEventListener('click', pendingPathGo);
    $('pending-path-input').addEventListener('keydown', function (e) {
      if (e.key === 'Enter') { e.preventDefault(); pendingPathGo(); }
      /* Esc 只清输入框，不往上冒 —— 冒上去会被全局 keydown 当成"关掉最上层
         弹窗"，把用户正在看的树 / 面板一起关了。 */
      else if (e.key === 'Escape') { e.stopPropagation(); this.value = ''; }
    });
    $('drop-file-clear').addEventListener('click', clearDropFile);
    $('btn-run').addEventListener('click', runPatch);
    $('btn-settings').addEventListener('click', openSettings);
    $('btn-settings-apply').addEventListener('click', applySettings);
    $('btn-settings-close').addEventListener('click', function () {
      $('settings-overlay').hidden = true;
    });
    /* 点遮罩不关（见上面确认框那段）：设置面板里改了一半被误关掉最气人。 */

    // 查看签名 / 查看绑定信息两个弹窗
    $('sign-close').addEventListener('click', function () { $('sign-overlay').hidden = true; });
    /* 点遮罩不关（见上面确认框那段） */
    var closeMapped = function () { $('mapped-overlay').hidden = true; };
    $('mapped-x').addEventListener('click', closeMapped);
    $('mapped-close').addEventListener('click', closeMapped);
    $('mapped-save').addEventListener('click', function () {
      runVoid('SaveMappedInfoAs', [currentMappedPath], '已保存');
    });
    $('mapped-copy').addEventListener('click', function () {
      var btn = $('mapped-copy');
      call('CopyMappedInfo', currentMappedPath)
        .then(function () {
          // 对齐原版：拷完按钮短暂变成"已拷贝！！"并置灰，3 秒后复原
          btn.disabled = true;
          btn.textContent = '已拷贝！！';
          setTimeout(function () {
            btn.disabled = false;
            btn.textContent = '拷贝信息';
          }, 3000);
        })
        .catch(function (e) { toast(errText(e), true); });
    });

    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape') {
        /* 确认框在最上层，先收它 —— 否则 Esc 会把下面的设置/签名框一起关掉，
           而确认框自己还挂着，Promise 永远不 resolve。 */
        if (confirmPending) { confirmPending(false); return; }
        /* 「拖入待替换」清单不是弹窗、也没挡住任何东西，Esc 不管它 ——
           它的出口是「取消」按钮。这里要是也顺手清掉，用户在设置框里按 Esc
           会把刚拖进来的匹配结果一起丢了。 */
        $('settings-overlay').hidden = true;
        $('sign-overlay').hidden = true;
        $('mapped-overlay').hidden = true;
        compareClose();
        if (search.open) searchClose();
        hideMenu();
        // 内容预览是浮动面板，Esc 关掉它；没开的时候别把别的弹窗连坐
        if (!$('content-win').hidden) hideContentWin();
      }
    });

    // 拉一次初始状态（内含默认设置）
    call('GetState').then(apply).catch(function () {});

    initDrop();
    initLinks();
    initWindowControls();
    initContextMenu();
    initContentWin();
    initEditorWin();
    initSplitters();
    initFastSearch($('app-tree'), 'app');
    initFastSearch($('patch-tree'), 'patch');
    initFindBar();
    initSearch();
    initCompare();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();

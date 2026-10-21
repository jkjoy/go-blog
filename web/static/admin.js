async function adminResponseData(response) {
  "use strict";
  var type = response.headers.get("content-type") || "";
  if (type.indexOf("application/json") !== -1) return response.json();
  var text = await response.text();
  return { message: text.trim() };
}

function markdownLabel(value) {
  "use strict";
  return value.replace(/\\/g, "\\\\").replace(/\[/g, "\\[").replace(/\]/g, "\\]");
}

(function () {
  "use strict";
  var picker = document.getElementById("attachment-file");
  var editor = document.getElementById("content-editor");
  var status = document.getElementById("attachment-status");
  var control = document.querySelector(".attachment-control");
  if (!picker || !editor || !status || !control) return;

  picker.addEventListener("change", async function () {
    var file = picker.files && picker.files[0];
    if (!file) return;
    status.textContent = "上传中...";
    picker.disabled = true;
    var body = new FormData();
    body.append("csrf", control.dataset.csrf);
    body.append("file", file);
    try {
      var response = await fetch("/admin/upload", { method: "POST", body: body, credentials: "same-origin" });
      var result = await adminResponseData(response);
      if (!response.ok) throw new Error(result.message || "上传失败");
      var image = file.type.indexOf("image/") === 0;
      var label = markdownLabel(file.name);
      var markdown = image ? "![" + label + "](" + result.url + ")" : "[" + label + "](" + result.url + ")";
      var start = editor.selectionStart;
      var before = editor.value.slice(0, start);
      var after = editor.value.slice(editor.selectionEnd);
      var prefix = before && !before.endsWith("\n") ? "\n\n" : "";
      editor.value = before + prefix + markdown + "\n" + after;
      editor.dispatchEvent(new Event("input", { bubbles: true }));
      editor.focus();
      editor.selectionStart = editor.selectionEnd = (before + prefix + markdown + "\n").length;
      status.textContent = "已插入 " + file.name;
    } catch (error) {
      status.textContent = error.message || "上传失败";
    } finally {
      picker.value = "";
      picker.disabled = false;
    }
  });
})();

(function () {
  "use strict";
  document.addEventListener("submit", function (event) {
    var submitter = event.submitter;
    var message = (submitter && submitter.dataset.confirm) || event.target.dataset.confirm;
    if (message && !window.confirm(message)) event.preventDefault();
  });

  var form = document.querySelector("form.editor");
  if (!form) return;
  var dirty = false;
  var submitting = false;
  form.addEventListener("input", function () { dirty = true; });
  form.addEventListener("change", function () { dirty = true; });
  form.addEventListener("submit", function (event) {
    if (!event.defaultPrevented) submitting = true;
  });
  window.addEventListener("beforeunload", function (event) {
    if (!dirty || submitting) return;
    event.preventDefault();
    event.returnValue = "";
  });
})();

(function () {
  "use strict";
  var control = document.querySelector(".ai-control");
  var buttons = Array.prototype.slice.call(document.querySelectorAll(".ai-action"));
  var title = document.querySelector('[name="title"]');
  var slug = document.querySelector('[name="slug"]');
  var excerpt = document.querySelector('[name="excerpt"]');
  var editor = document.getElementById("content-editor");
  var status = document.getElementById("ai-status");
  if (!control || control.dataset.ready !== "1" || !buttons.length || !title || !slug || !excerpt || !editor || !status) return;

  var labels = { summary: "正在生成摘要...", slug: "正在翻译 Slug...", polish: "正在润色正文..." };

  buttons.forEach(function (button) {
    button.addEventListener("click", async function () {
      var action = button.dataset.aiAction;
      var source = editor.value;
      var start = editor.selectionStart;
      var end = editor.selectionEnd;
      var selected = action === "polish" && end > start;
      var content = selected ? source.slice(start, end) : source;
      var instruction = "";
      if (action === "polish") {
        instruction = window.prompt("润色要求（可选）", "");
        if (instruction === null) return;
      }

      var body = new FormData();
      body.append("csrf", control.dataset.csrf);
      body.append("action", action);
      body.append("title", title.value);
      body.append("content", content);
      body.append("instruction", instruction);
      buttons.forEach(function (item) { item.disabled = true; });
      status.textContent = labels[action] || "AI 处理中...";

      try {
        var response = await fetch("/admin/ai", { method: "POST", body: body, credentials: "same-origin" });
        var result = await adminResponseData(response);
        if (!response.ok) throw new Error(result.message || "AI 操作失败");
        if (action === "summary") {
          excerpt.value = result.result;
          excerpt.dispatchEvent(new Event("input", { bubbles: true }));
        } else if (action === "slug") {
          slug.value = result.result;
          slug.dispatchEvent(new Event("input", { bubbles: true }));
        } else {
          if (editor.value !== source) throw new Error("正文已发生修改，润色结果未应用");
          var message = selected ? "使用润色结果替换选中的正文？" : "使用润色结果替换完整正文？";
          if (!window.confirm(message)) {
            status.textContent = "已取消应用润色结果";
            return;
          }
          editor.value = selected ? source.slice(0, start) + result.result + source.slice(end) : result.result;
          editor.dispatchEvent(new Event("input", { bubbles: true }));
          editor.focus();
        }
        status.textContent = "AI 操作已完成";
      } catch (error) {
        status.textContent = error.message || "AI 操作失败";
      } finally {
        buttons.forEach(function (item) { item.disabled = false; });
      }
    });
  });
})();

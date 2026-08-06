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
      var result = await response.json();
      if (!response.ok) throw new Error(result.message || "上传失败");
      var image = file.type.indexOf("image/") === 0;
      var markdown = image ? "![" + file.name + "](" + result.url + ")" : "[" + file.name + "](" + result.url + ")";
      var start = editor.selectionStart;
      var before = editor.value.slice(0, start);
      var after = editor.value.slice(editor.selectionEnd);
      var prefix = before && !before.endsWith("\n") ? "\n\n" : "";
      editor.value = before + prefix + markdown + "\n" + after;
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

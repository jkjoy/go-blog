(function () {
  "use strict";

  var root = document.documentElement;
  root.classList.add("js");
  var reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)");
  var mobile = window.matchMedia("(max-width: 650px)");
  var themeButton = document.getElementById("theme-toggle");
  var themeColor = document.querySelector('meta[name="theme-color"]');
  var menuButton = document.getElementById("menu-toggle");
  var nav = document.getElementById("site-nav");
  var overlay = document.getElementById("menu-overlay");
  var backTop = document.getElementById("back-top");
  var progress = document.getElementById("reading-progress");

  function applyTheme(theme) {
    if (theme !== "light" && theme !== "dark") return;
    root.dataset.theme = theme;
    if (themeColor) themeColor.content = theme === "dark" ? "#171c28" : "#ffffff";
    if (themeButton) {
      var label = theme === "dark" ? "切换到浅色模式" : "切换到深色模式";
      themeButton.setAttribute("aria-label", label);
      themeButton.title = label;
    }
  }

  applyTheme(root.dataset.theme === "dark" ? "dark" : "light");
  if (themeButton) {
    themeButton.addEventListener("click", function () {
      var next = root.dataset.theme === "dark" ? "light" : "dark";
      applyTheme(next);
      try { localStorage.setItem("sunny-theme", next); } catch (_) {}
    });
  }
  window.addEventListener("storage", function (event) {
    if (event.key === "sunny-theme") applyTheme(event.newValue);
  });

  document.querySelectorAll(".site-logo img, .avatar img, .link-avatar img, .post-image img").forEach(function (image) {
    function showImageFallback() {
      var cover = image.closest(".post-image");
      if (cover) cover.hidden = true;
      else image.hidden = true;
    }
    image.addEventListener("error", showImageFallback);
    if (image.complete && image.naturalWidth === 0) showImageFallback();
  });

  if (menuButton && nav) {
    function menuIsOpen() {
      return menuButton.getAttribute("aria-expanded") === "true";
    }

    function setMenu(open, restoreFocus) {
      open = open && mobile.matches;
      menuButton.setAttribute("aria-expanded", String(open));
      menuButton.setAttribute("aria-label", open ? "关闭导航" : "打开导航");
      nav.classList.toggle("open", open);
      document.body.classList.toggle("menu-open", open);
      nav.inert = mobile.matches && !open;
      if (overlay) overlay.hidden = !open;
      if (open) {
        window.requestAnimationFrame(function () {
          if (!mobile.matches || !menuIsOpen()) return;
          var firstLink = nav.querySelector("a[href], button:not([disabled]), summary");
          if (firstLink) firstLink.focus({ preventScroll: true });
        });
      } else if (restoreFocus) {
        menuButton.focus({ preventScroll: true });
      }
    }

    menuButton.addEventListener("click", function () { setMenu(!menuIsOpen(), false); });
    if (overlay) overlay.addEventListener("click", function () { setMenu(false, true); });
    nav.addEventListener("click", function (event) {
      if (event.target.closest("a[href]")) setMenu(false, false);
    });
    document.addEventListener("keydown", function (event) {
      if (!mobile.matches || !menuIsOpen()) return;
      if (event.key === "Escape") {
        event.preventDefault();
        setMenu(false, true);
      } else if (event.key === "Tab") {
        var items = Array.prototype.slice.call(nav.querySelectorAll(
          'a[href], button:not([disabled]), input:not([type="hidden"]):not([disabled]), summary, [tabindex="0"]'
        )).filter(function (item) { return item.getClientRects().length > 0; });
        items.push(menuButton);
        var activeIndex = items.indexOf(document.activeElement);
        var nextIndex = event.shiftKey ? activeIndex - 1 : activeIndex + 1;
        if (nextIndex < 0) nextIndex = items.length - 1;
        if (nextIndex >= items.length) nextIndex = 0;
        event.preventDefault();
        items[nextIndex].focus();
      }
    });
    mobile.addEventListener("change", function () { setMenu(false, false); });
    setMenu(false, false);
  }

  if (backTop) {
    var progressPending = false;
    function updateProgress() {
      progressPending = false;
      var distance = Math.max(0, root.scrollHeight - window.innerHeight);
      var percent = distance > 0 ? Math.round(Math.max(0, Math.min(1, window.scrollY / distance)) * 100) : 0;
      if (progress) progress.textContent = percent + "%";
      backTop.classList.toggle("visible", window.scrollY > 80);
      backTop.setAttribute("aria-label", "回到顶部，已阅读 " + percent + "%");
    }
    function scheduleProgress() {
      if (progressPending) return;
      progressPending = true;
      window.requestAnimationFrame(updateProgress);
    }
    window.addEventListener("scroll", scheduleProgress, { passive: true });
    window.addEventListener("resize", scheduleProgress, { passive: true });
    window.addEventListener("load", scheduleProgress);
    backTop.addEventListener("click", function () {
      window.scrollTo({ top: 0, behavior: reduceMotion.matches ? "auto" : "smooth" });
    });
    updateProgress();
  }

  var parentInput = document.getElementById("parent-id");
  var cancelReply = document.getElementById("cancel-reply");
  var commentText = document.querySelector(".comment-form textarea");
  if (parentInput && cancelReply && commentText) {
    var originalPlaceholder = commentText.placeholder;
    document.querySelectorAll("[data-reply-id]").forEach(function (button) {
      button.addEventListener("click", function () {
        parentInput.value = button.dataset.replyId;
        cancelReply.hidden = false;
        commentText.placeholder = "回复 @" + (button.dataset.replyName || "评论者");
        commentText.focus({ preventScroll: true });
        commentText.scrollIntoView({ behavior: reduceMotion.matches ? "auto" : "smooth", block: "center" });
      });
    });
    cancelReply.addEventListener("click", function () {
      parentInput.value = "";
      cancelReply.hidden = true;
      commentText.placeholder = originalPlaceholder;
      commentText.focus({ preventScroll: true });
    });
  }

  document.querySelectorAll(".prose pre").forEach(function (pre) {
    var code = pre.querySelector("code");
    if (!code) return;
    var block = document.createElement("div");
    block.className = "code-block";
    var toolbar = document.createElement("div");
    toolbar.className = "code-toolbar";
    var dots = document.createElement("span");
    dots.className = "code-dots";
    dots.setAttribute("aria-hidden", "true");
    for (var i = 0; i < 3; i++) dots.appendChild(document.createElement("i"));
    var language = document.createElement("span");
    language.className = "code-language";
    var match = code.className.match(/(?:^|\s)language-([\w-]+)/);
    language.textContent = match ? match[1] : "code";
    var copy = document.createElement("button");
    copy.type = "button";
    copy.className = "copy-code";
    copy.textContent = "复制";
    copy.setAttribute("aria-label", "复制代码");
    var status = document.createElement("span");
    status.className = "copy-status sr-only";
    status.setAttribute("role", "status");
    var resetTimer;
    copy.addEventListener("click", async function () {
      window.clearTimeout(resetTimer);
      copy.disabled = true;
      try {
        if (!navigator.clipboard || !navigator.clipboard.writeText) throw new Error("Clipboard unavailable");
        await navigator.clipboard.writeText(code.textContent || "");
        copy.textContent = "已复制";
        status.textContent = "代码已复制";
      } catch (_) {
        copy.textContent = "复制失败";
        status.textContent = "复制失败，请手动选择并复制代码。";
      } finally {
        copy.disabled = false;
        resetTimer = window.setTimeout(function () { copy.textContent = "复制"; }, 2500);
      }
    });
    toolbar.append(dots, language, copy, status);
    pre.parentNode.insertBefore(block, pre);
    block.append(toolbar, pre);
  });

  var toc = document.getElementById("article-toc");
  var tocCard = document.getElementById("toc-card");
  if (toc && tocCard) {
    var headings = document.querySelectorAll(".prose h2[id], .prose h3[id]");
    headings.forEach(function (heading) {
      var item = document.createElement("li");
      if (heading.tagName === "H3") item.className = "toc-subheading";
      var link = document.createElement("a");
      link.href = "#" + encodeURIComponent(heading.id);
      link.textContent = heading.textContent;
      item.appendChild(link);
      toc.appendChild(item);
    });
    tocCard.hidden = headings.length === 0;
  }
})();

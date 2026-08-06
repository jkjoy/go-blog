(function () {
  "use strict";

  var root = document.documentElement;
  var reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  var themeButton = document.getElementById("theme-toggle");
  var menuButton = document.getElementById("menu-toggle");
  var nav = document.getElementById("site-nav");
  var backTop = document.getElementById("back-top");

  if (themeButton) {
    themeButton.addEventListener("click", function () {
      var next = root.dataset.theme === "light" ? "dark" : "light";
      root.dataset.theme = next;
      try { localStorage.setItem("nebula-theme", next); } catch (_) {}
    });
  }

  if (menuButton && nav) {
    function closeMenu() {
      menuButton.setAttribute("aria-expanded", "false");
      menuButton.setAttribute("aria-label", "打开导航");
      nav.classList.remove("open");
    }
    menuButton.addEventListener("click", function () {
      var open = menuButton.getAttribute("aria-expanded") !== "true";
      menuButton.setAttribute("aria-expanded", String(open));
      menuButton.setAttribute("aria-label", open ? "关闭导航" : "打开导航");
      nav.classList.toggle("open", open);
    });
    nav.addEventListener("click", function (event) { if (event.target.closest("a")) closeMenu(); });
    document.addEventListener("click", function (event) {
      if (!nav.contains(event.target) && !menuButton.contains(event.target)) closeMenu();
    });
  }

  var reveals = Array.prototype.slice.call(document.querySelectorAll(".reveal"));
  if (reduceMotion || !("IntersectionObserver" in window)) {
    reveals.forEach(function (item) { item.classList.add("visible"); });
  } else {
    var observer = new IntersectionObserver(function (entries) {
      entries.forEach(function (entry) {
        if (entry.isIntersecting) {
          entry.target.classList.add("visible");
          observer.unobserve(entry.target);
        }
      });
    }, { rootMargin: "0px 0px -5%", threshold: 0.08 });
    reveals.forEach(function (item) { observer.observe(item); });
  }

  if (backTop) {
    function updateBackTop() { backTop.classList.toggle("visible", window.scrollY > 520); }
    window.addEventListener("scroll", updateBackTop, { passive: true });
    backTop.addEventListener("click", function () { window.scrollTo({ top: 0, behavior: reduceMotion ? "auto" : "smooth" }); });
    updateBackTop();
  }

  var parentInput = document.getElementById("parent-id");
  var cancelReply = document.getElementById("cancel-reply");
  document.querySelectorAll("[data-reply-id]").forEach(function (button) {
    button.addEventListener("click", function () {
      if (!parentInput) return;
      parentInput.value = button.dataset.replyId;
      cancelReply.hidden = false;
      var textarea = document.querySelector(".comment-form textarea");
      if (textarea) {
        textarea.placeholder = "回复 @" + button.dataset.replyName;
        textarea.focus({ preventScroll: true });
        textarea.scrollIntoView({ behavior: reduceMotion ? "auto" : "smooth", block: "center" });
      }
    });
  });
  if (cancelReply) {
    cancelReply.addEventListener("click", function () {
      parentInput.value = "";
      cancelReply.hidden = true;
      var textarea = document.querySelector(".comment-form textarea");
      if (textarea) textarea.placeholder = "";
    });
  }

  if (!reduceMotion) startStars();

  function startStars() {
    var canvas = document.getElementById("stars");
    if (!canvas) return;
    var context = canvas.getContext("2d");
    if (!context) return;
    var stars = [];
    var width = 0;
    var height = 0;
    var ratio = Math.min(window.devicePixelRatio || 1, 2);

    function resize() {
      width = window.innerWidth;
      height = window.innerHeight;
      canvas.width = width * ratio;
      canvas.height = height * ratio;
      canvas.style.width = width + "px";
      canvas.style.height = height + "px";
      context.setTransform(ratio, 0, 0, ratio, 0, 0);
      var count = Math.min(120, Math.max(45, Math.floor(width * height / 14000)));
      stars = Array.from({ length: count }, function () {
        return { x: Math.random() * width, y: Math.random() * height, r: Math.random() * 1.15 + .2, a: Math.random() * .6 + .18, v: Math.random() * .08 + .025 };
      });
    }
    function frame() {
      context.clearRect(0, 0, width, height);
      var light = root.dataset.theme === "light";
      stars.forEach(function (star) {
        star.y -= star.v;
        if (star.y < -2) { star.y = height + 2; star.x = Math.random() * width; }
        context.beginPath();
        context.fillStyle = light ? "rgba(25,38,48," + star.a * .42 + ")" : "rgba(225,248,246," + star.a + ")";
        context.arc(star.x, star.y, star.r, 0, Math.PI * 2);
        context.fill();
      });
      window.requestAnimationFrame(frame);
    }
    window.addEventListener("resize", resize, { passive: true });
    resize();
    frame();
  }
})();

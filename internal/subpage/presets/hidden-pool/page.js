(function () {
  var $ = function (id) { return document.getElementById(id); };

  /* копирование: страница работает в песочнице, поэтому старый способ запасной */
  var toastTimer;
  function toast(t) { var el = $('toast'); el.textContent = t; el.classList.add('on'); clearTimeout(toastTimer); toastTimer = setTimeout(function () { el.classList.remove('on'); }, 1600); }
  function copy(text, done) {
    function old() { var t = document.createElement('textarea'); t.value = text; document.body.appendChild(t); t.select(); try { document.execCommand('copy'); done(); } catch (e) {} t.remove(); }
    if (navigator.clipboard) navigator.clipboard.writeText(text).then(done, old); else old();
  }
  document.addEventListener('click', function (e) {
    var b = e.target.closest('[data-copy]'); if (!b) return;
    var lbl = b.querySelector('.lbl'), was = lbl && lbl.textContent;
    copy(b.getAttribute('data-copy'), function () {
      toast(b.getAttribute('data-toast') || 'Ссылка скопирована');
      if (lbl) { lbl.textContent = 'Скопировано'; setTimeout(function () { lbl.textContent = was; }, 1600); }
    });
  });

  /* устройства: откуда взять приложение */
  var OS = {
    ios: ['Установите приложение', 'Откройте App Store и установите Happ. Это бесплатно.'],
    android: ['Установите приложение', 'Откройте Google Play и установите Happ. Это бесплатно.'],
    win: ['Скачайте приложение', 'Скачайте установщик Happ для Windows с сайта приложения и запустите его.'],
    mac: ['Скачайте приложение', 'Скачайте Happ для macOS с сайта приложения или из App Store и откройте его.']
  };
  var tabs = $('tabs');
  function setOs(os) {
    if (!tabs) return;
    var btn = tabs.querySelector('[data-os="' + os + '"]'), thumb = tabs.querySelector('.thumb');
    tabs.querySelectorAll('button').forEach(function (b) { b.setAttribute('aria-selected', b === btn); });
    thumb.style.width = btn.offsetWidth + 'px'; thumb.style.left = '4px'; thumb.style.transform = 'translateX(' + (btn.offsetLeft - 4) + 'px)';
    $('s1t').textContent = OS[os][0]; $('s1p').textContent = OS[os][1];
  }
  if (tabs) {
    tabs.addEventListener('click', function (e) { var b = e.target.closest('button'); if (b) setOs(b.dataset.os); });
    var ua = navigator.userAgent;
    setOs(/iPhone|iPad/.test(ua) ? 'ios' : /Android/.test(ua) ? 'android' : /Mac/.test(ua) ? 'mac' : 'win');
    addEventListener('resize', function () { setOs(tabs.querySelector('[aria-selected=true]').dataset.os); });
  }

  /* ноды: флаг из названия выносится в квадрат слева */
  var flagRe = /^([\u{1F1E6}-\u{1F1FF}]{2})\s*/u;
  document.querySelectorAll('.node').forEach(function (n) {
    var b = n.querySelector('b'), f = n.querySelector('.flag'); if (!b || !f) return;
    var m = b.textContent.match(flagRe); if (m) { f.textContent = m[1]; b.textContent = b.textContent.replace(flagRe, ''); }
  });
  var toggle = $('nodesToggle');
  if (toggle) toggle.addEventListener('click', function () {
    var open = this.getAttribute('aria-expanded') !== 'true';
    this.setAttribute('aria-expanded', open); $('nodesPanel').classList.toggle('is-open', open);
  });

  /* появление при прокрутке */
  var rv = document.querySelectorAll('.rv');
  if ('IntersectionObserver' in window && !matchMedia('(prefers-reduced-motion: reduce)').matches) {
    var io = new IntersectionObserver(function (es) { es.forEach(function (e) { if (e.isIntersecting) { e.target.classList.add('in'); io.unobserve(e.target); } }); }, { threshold: .12 });
    rv.forEach(function (el, i) { el.style.transitionDelay = (i % 3) * 70 + 'ms'; io.observe(el); });
  } else rv.forEach(function (el) { el.classList.add('in'); });
})();

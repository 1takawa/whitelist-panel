// Copy buttons: the text to copy is in data-copy. The page runs in a sandbox, so the old way of copying is the fallback.
document.querySelectorAll('[data-copy]').forEach(function (b) {
  b.addEventListener('click', function () {
    var text = b.getAttribute('data-copy'), was = b.textContent;
    function done() { b.textContent = 'Скопировано'; setTimeout(function () { b.textContent = was; }, 1500); }
    function old() {
      var t = document.createElement('textarea'); t.value = text; document.body.appendChild(t); t.select();
      try { document.execCommand('copy'); done(); } catch (e) {} t.remove();
    }
    if (navigator.clipboard) navigator.clipboard.writeText(text).then(done, old); else old();
  });
});

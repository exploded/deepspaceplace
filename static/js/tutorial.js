/* Tutorial runbook behaviour: figure lightbox, TOC scroll-spy, linear/non-linear
   state rail. Loaded with defer by templates/pages/tutorial-*.html. */
(function () {
  var root = document.querySelector('.tutorial');
  if (!root) return;

  // ---- lightbox ----------------------------------------------------------
  // Appended to <body>, not to .tutorial, so it escapes the .container
  // stacking context and can sit above the navbar dropdowns.
  var lb = document.createElement('div');
  lb.className = 'tutorial-lb';
  var lbImg = document.createElement('img');
  lb.appendChild(lbImg);
  document.body.appendChild(lb);

  root.querySelectorAll('figure img').forEach(function (im) {
    im.addEventListener('click', function () {
      lbImg.src = im.src;
      lbImg.alt = im.alt;
      lb.classList.add('on');
    });
  });
  lb.addEventListener('click', function () { lb.classList.remove('on'); });
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') lb.classList.remove('on');
  });

  // ---- TOC scroll-spy ----------------------------------------------------
  var links = [].slice.call(root.querySelectorAll('.rail nav a'));
  var targets = links
    .map(function (a) { return document.getElementById(a.hash.slice(1)); })
    .filter(Boolean);

  if (targets.length) {
    var spy = new IntersectionObserver(function (entries) {
      entries.forEach(function (e) {
        if (!e.isIntersecting) return;
        links.forEach(function (l) { l.classList.remove('active'); });
        var m = links.find(function (l) { return l.hash.slice(1) === e.target.id; });
        if (m) {
          m.classList.add('active');
          m.scrollIntoView({ block: 'nearest' });
        }
      });
    }, { rootMargin: '0px 0px -78% 0px' });
    targets.forEach(function (t) { spy.observe(t); });
  }

  // ---- state rail --------------------------------------------------------
  // Tracks whether the data is linear or stretched at this point in the
  // document. Only the standard workflow carries [data-state] markers; the
  // mosaic runbook ends before the stretch, so it stays linear throughout.
  var bar = root.querySelector('.state');
  var tag = root.querySelector('.state-tag');
  if (bar && tag) {
    var marks = [].slice.call(root.querySelectorAll('[data-state]'));
    var setState = function (s) {
      var nl = s === 'nonlinear';
      bar.classList.toggle('nonlinear', nl);
      tag.classList.toggle('nonlinear', nl);
      tag.textContent = nl ? 'non-linear' : 'linear';
    };
    var onScroll = function () {
      var cur = 'linear';
      for (var i = 0; i < marks.length; i++) {
        if (marks[i].getBoundingClientRect().top < window.innerHeight * 0.45) {
          cur = marks[i].dataset.state;
        }
      }
      setState(cur);
    };
    window.addEventListener('scroll', onScroll, { passive: true });
    onScroll();
  }
})();

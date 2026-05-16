// TallyWhatsApp landing page — minimal client-side enhancement.
// Anything that needs JS goes here. Anything that doesn't is in HTML/CSS.
// Loaded with `defer`, never blocks render.

(function () {
  'use strict';

  // Smooth-scroll polyfill is unnecessary — `scroll-behavior: smooth` in CSS
  // covers modern browsers and degrades to instant scroll on old ones.

  // 1. Close other open <details> when one opens. Keeps the FAQ tidy.
  const faqDetails = document.querySelectorAll('.faq details');
  faqDetails.forEach((d) => {
    d.addEventListener('toggle', () => {
      if (d.open) {
        faqDetails.forEach((other) => {
          if (other !== d && other.open) other.open = false;
        });
      }
    });
  });

  // 2. Highlight the nav link for the current section as the user scrolls.
  // Pure visual nicety; falls back to nothing if IntersectionObserver is missing.
  if ('IntersectionObserver' in window) {
    const sections = document.querySelectorAll('section[id]');
    const navLinks = document.querySelectorAll('nav[aria-label="Primary"] a[href^="#"]');
    if (sections.length && navLinks.length) {
      const linkByHash = new Map();
      navLinks.forEach((a) => linkByHash.set(a.getAttribute('href'), a));
      const obs = new IntersectionObserver(
        (entries) => {
          entries.forEach((e) => {
            if (e.isIntersecting) {
              const href = '#' + e.target.id;
              navLinks.forEach((a) => a.classList.remove('active'));
              const link = linkByHash.get(href);
              if (link) link.classList.add('active');
            }
          });
        },
        { rootMargin: '-40% 0px -55% 0px' }
      );
      sections.forEach((s) => obs.observe(s));
    }
  }
})();

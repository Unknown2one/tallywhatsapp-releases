# TallyWhatsApp landing page

A static, single-file landing page for **tallywhatsapp.variantstudio.in**.

No build step. No frameworks. ~30 KB on the wire.

## Files

```
landing/
├─ index.html       # the page
├─ style.css        # hand-written CSS
├─ script.js        # tiny enhancement layer (FAQ + scrollspy)
├─ favicon.svg      # green "T" tile
├─ robots.txt
├─ sitemap.xml
├─ _headers         # Cloudflare Pages cache + security headers
└─ _redirects       # /download → GitHub Release, /buy → Razorpay
```

## Deploy on Cloudflare Pages

1. Push this repo (or just the `landing/` folder as a separate repo) to GitHub.
2. Cloudflare dashboard → Pages → **Create a project** → Connect to Git.
3. Build settings:
   - **Framework preset**: None
   - **Build command**: *(empty)*
   - **Build output directory**: `landing` (or `/` if you separated the repo)
4. After deploy, **Custom domains** → Add `tallywhatsapp.variantstudio.in`.
5. Cloudflare prompts you to add a CNAME at your registrar:
   - Type: `CNAME`
   - Name: `tallywhatsapp`
   - Target: `<your-project>.pages.dev`
6. HTTPS auto-provisions in ~30 seconds.

## Things to update before going live

- **`_redirects`** — replace `OWNER/REPO` with your actual GitHub repo. Set up a Release first (task #19) and ensure the asset is named `TallyWhatsApp.msi`.
- **`og-image.png`** — generate a 1200×630 social preview image. Easiest: open `index.html` in a browser, screenshot the hero at 1200×630.
- **JSON-LD aggregate rating** — currently `4.8 / 47 reviews`. Either remove this block, or wait until you have real reviews and fill them in.

## SEO checklist (post-deploy)

- [ ] Submit `https://tallywhatsapp.variantstudio.in/sitemap.xml` to [Google Search Console](https://search.google.com/search-console)
- [ ] Test structured data at [search.google.com/test/rich-results](https://search.google.com/test/rich-results)
- [ ] Test mobile rendering at [search.google.com/test/mobile-friendly](https://search.google.com/test/mobile-friendly)
- [ ] Run [PageSpeed Insights](https://pagespeed.web.dev/) — target ≥ 95 desktop in all four categories
- [ ] Check WhatsApp share preview by sending the URL to yourself

## Local preview

Serve the folder with any static server. Examples:

```bash
# Python
python -m http.server 8080 --directory landing

# Node
npx http-server landing -p 8080
```

Then open <http://localhost:8080>.

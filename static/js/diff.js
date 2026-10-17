import { App } from './state.js';
import { esc } from './utils.js';

/**
 * Открывает модальное окно сравнения двух постов бок о бок.
 * @param {any} postA
 * @param {any} postB
 */
App.openCompareModal = function (postA, postB) {
  if (!postA || !postB) return;
  const modal = document.getElementById('diff-modal');
  const closeBtn = document.getElementById('diff-close');
  const imgA = /** @type {HTMLImageElement | null} */ (document.getElementById('diff-img-a'));
  const imgB = /** @type {HTMLImageElement | null} */ (document.getElementById('diff-img-b'));
  const metaA = document.getElementById('diff-meta-a');
  const metaB = document.getElementById('diff-meta-b');

  if (!modal || !imgA || !imgB || !metaA || !metaB) return;

  const urlFor = (p) => {
    if (p.downloaded) return `/api/file/${p.id}`;
    if (p.file_url) return `/api/proxy?url=${encodeURIComponent(p.file_url)}`;
    if (p.sample_url) return `/api/proxy?url=${encodeURIComponent(p.sample_url)}`;
    return `/api/thumb/${p.id}`;
  };

  imgA.src = urlFor(postA);
  imgB.src = urlFor(postB);

  const formatMeta = (p) => {
    const size = p.file_size ? `${Math.round(p.file_size / 1024)} KB` : '';
    const dims = p.width && p.height ? `${p.width}×${p.height}` : '';
    const rating = p.rating ? p.rating.toUpperCase() : '';
    return `<strong>#${esc(p.id)}</strong> &middot; ${[dims, size, rating, esc(p.file_type || '')].filter(Boolean).join(' &middot; ')}`;
  };

  metaA.innerHTML = formatMeta(postA);
  metaB.innerHTML = formatMeta(postB);

  modal.classList.remove('hidden');

  const close = () => {
    modal.classList.add('hidden');
    imgA.src = '';
    imgB.src = '';
    document.removeEventListener('keydown', onKey);
  };

  const onKey = (e) => {
    if (e.key === 'Escape') {
      e.stopPropagation();
      close();
    }
  };

  if (closeBtn) {
    closeBtn.onclick = close;
  }
  modal.onclick = (e) => {
    if (e.target === modal) close();
  };
  document.addEventListener('keydown', onKey);
};

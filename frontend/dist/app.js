(() => {
  'use strict';

  const token = new URLSearchParams(location.search).get('t') || '';
  const state = {
    boot: null,
    cases: [],
    view: 'gallery',
    category: 'all',
    tag: '',
    query: '',
    photo: null,
    avatar: null,
    refs: [],
    history: [],
    status: null,
    modalCase: null,
    modalLang: 'en',
    historyQuery: '',
    historySource: '',
    historyStarred: false,
  };

  const $ = (id) => document.getElementById(id);
  const el = (tag, cls, text) => {
    const n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text !== undefined) n.textContent = text;
    return n;
  };
  const withToken = (url) => url + (url.includes('?') ? '&' : '?') + 't=' + encodeURIComponent(token);

  async function api(path, options = {}) {
    const headers = Object.assign({'X-002M-Token': token}, options.headers || {});
    if (options.body && !headers['Content-Type']) headers['Content-Type'] = 'application/json';
    const res = await fetch(path, Object.assign({}, options, {headers}));
    let data = null;
    try { data = await res.json(); } catch (_) { data = null; }
    if (!res.ok) {
      const msg = (data && (data.error || data.message)) || ('HTTP ' + res.status);
      throw new Error(msg);
    }
    return data;
  }

  function toast(message, isError = false) {
    const t = $('toast');
    t.textContent = message;
    t.classList.toggle('is-error', isError);
    t.classList.add('is-show');
    clearTimeout(toast._timer);
    toast._timer = setTimeout(() => t.classList.remove('is-show'), 4200);
  }

  function setHint(id, text, kind) {
    const n = $(id);
    n.textContent = text;
    n.classList.toggle('is-error', kind === 'error');
    n.classList.toggle('is-ok', kind === 'ok');
  }

  function formatTime(ms) {
    try {
      return new Date(ms).toLocaleString('zh-CN', {year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit'});
    } catch (_) { return ''; }
  }

  function shortText(s, n) {
    s = (s || '').replace(/\s+/g, ' ').trim();
    return s.length > n ? s.slice(0, n) + '…' : s;
  }

  function currentStatus(s) {
    state.status = s || null;
    const free = s ? (s.freeLeft != null ? s.freeLeft : s.trialLeft || 0) : null;
    const credit = s ? (s.credit || 0) : null;
    $('sideFree').textContent = free == null ? '—' : free;
    $('sideCredit').textContent = credit == null ? '—' : credit;
    $('quotaText').textContent = s ? ('免费 ' + free + ' · 付费 ' + credit) : '额度不可用';
    $('quotaPill').classList.toggle('is-low', !!s && (free + credit) <= 0);
    $('settingFree').textContent = free == null ? '—' : free;
    $('settingCredit').textContent = credit == null ? '—' : credit;
  }

  function switchView(name) {
    state.view = name;
    document.querySelectorAll('.nav-item').forEach(b => b.classList.toggle('is-active', b.dataset.view === name));
    document.querySelectorAll('.view').forEach(v => v.classList.toggle('is-active', v.dataset.view === name));
    const meta = {
      gallery: ['GALLERY', '案例画廊'],
      create: ['GENERATE', '照片生图'],
      faceswap: ['FACESWAP', '换脸套图'],
      history: ['ARCHIVE', '本地历史'],
      settings: ['SETTINGS', '设置'],
    }[name];
    if (meta) {
      $('pageKicker').textContent = meta[0];
      $('pageTitle').textContent = meta[1];
    }
  }

  // ---------- Gallery ----------
  function renderCategoryChips() {
    const wrap = $('categoryChips');
    wrap.textContent = '';
    const cats = (state.boot && state.boot.cases.categories) || {};
    const labels = {portrait: '人像', pet: '宠物', poster: '海报', scene: '场景', uncategorized: '其他'};
    const all = el('button', 'chip is-active', '全部');
    all.dataset.category = 'all';
    all.onclick = () => { state.category = 'all'; renderGallery(); };
    wrap.appendChild(all);
    Object.keys(cats).sort((a, b) => cats[b] - cats[a]).forEach(cat => {
      const b = el('button', 'chip', (labels[cat] || cat) + ' ' + cats[cat]);
      b.dataset.category = cat;
      b.onclick = () => { state.category = cat; renderGallery(); };
      wrap.appendChild(b);
    });
  }

  function renderTagFilter() {
    const sel = $('tagFilter');
    sel.textContent = '';
    sel.appendChild(new Option('全部标签', ''));
    const tags = (state.boot && state.boot.cases.tags) || {};
    Object.keys(tags).forEach(tag => sel.appendChild(new Option(tag + ' (' + tags[tag] + ')', tag)));
    sel.value = state.tag;
    sel.onchange = () => { state.tag = sel.value; renderGallery(); };
  }

  function caseMatches(c) {
    if (state.category !== 'all' && c.category !== state.category) return false;
    if (state.tag && !(c.tags || []).includes(state.tag)) return false;
    const q = state.query.trim().toLowerCase();
    if (!q) return true;
    const hay = [c.title, c.category, (c.tags || []).join(' '), c.prompt_zh, c.prompt_en].join(' ').toLowerCase();
    return hay.includes(q);
  }

  function renderGallery() {
    document.querySelectorAll('#categoryChips .chip').forEach(b => b.classList.toggle('is-active', b.dataset.category === state.category));
    const list = state.cases.filter(caseMatches);
    const grid = $('caseGrid');
    grid.textContent = '';
    const frag = document.createDocumentFragment();
    list.forEach(c => frag.appendChild(caseCard(c)));
    grid.appendChild(frag);
    $('caseResultCount').textContent = list.length + ' 条案例';
    const hints = [];
    if (state.category !== 'all') hints.push('分类：' + state.category);
    if (state.tag) hints.push('标签：' + state.tag);
    if (state.query) hints.push('搜索：' + state.query);
    $('caseFilterHint').textContent = hints.length ? hints.join(' · ') : '';
    $('caseEmpty').hidden = list.length > 0;
    $('caseSearchClear').hidden = !state.query;
    $('caseCountBadge').textContent = (state.boot ? state.boot.cases.count : list.length) + ' 条';
    $('caseSearch').value = state.query;
  }

  function caseCard(c) {
    const card = el('div', 'case-card');
    card.tabIndex = 0;
    card.onclick = () => openCase(c);
    card.onkeydown = (e) => { if (e.key === 'Enter') openCase(c); };
    const media = el('div', 'case-media');
    const img = el('img');
    img.loading = 'lazy';
    img.alt = c.title || c.id;
    img.src = withToken('/thumb/' + encodeURIComponent(c.id));
    img.onerror = () => {
      if (!img.dataset.retried) {
        img.dataset.retried = '1';
        setTimeout(() => { img.src = withToken('/thumb/' + encodeURIComponent(c.id)) + '&r=' + Date.now(); }, 900);
        return;
      }
      img.remove();
      const fb = el('div', 'fallback', '缩略图加载失败');
      media.appendChild(fb);
    };
    media.appendChild(img);
    media.appendChild(el('span', 'case-cat', c.category === 'portrait' ? '人像' : (c.category || 'case')));
    const info = el('div', 'case-info');
    info.appendChild(el('div', 'case-title', c.title || c.id));
    const tags = el('div', 'case-tags');
    (c.tags || []).slice(0, 2).forEach(t => tags.appendChild(el('span', 'tag', t)));
    info.appendChild(tags);
    card.appendChild(media);
    card.appendChild(info);
    return card;
  }

  function parseRemix(c) {
    if (Array.isArray(c.remix_variables)) return c.remix_variables;
    if (typeof c.remix_variables === 'string') {
      try { const x = JSON.parse(c.remix_variables); return Array.isArray(x) ? x : []; } catch (_) { return []; }
    }
    return [];
  }

  function openCase(c) {
    state.modalCase = c;
    state.modalLang = c.prompt_en ? 'en' : (c.prompt_zh ? 'zh' : 'en');
    $('modalImage').src = withToken('/case-image/' + encodeURIComponent(c.id));
    $('modalImage').alt = c.title || c.id;
    $('modalTitle').textContent = c.title || c.id;
    $('modalId').textContent = c.id;
    $('modalCategory').textContent = c.category || 'case';
    const tags = $('modalTags');
    tags.textContent = '';
    (c.tags || []).forEach(t => tags.appendChild(el('span', 'tag', t)));
    renderModalPrompt();
    const remix = $('modalRemix');
    remix.textContent = '';
    const vars = parseRemix(c);
    if (vars.length) {
      remix.appendChild(el('h3', '', 'Remix 变量'));
      vars.forEach(v => {
        const item = el('div', 'remix-item');
        item.appendChild(el('strong', '', (v.label || v.key || '') + '：'));
        item.appendChild(document.createTextNode((v.options || []).join(' / ') || String(v.default || '')));
        if (v.default) item.appendChild(el('span', '', '  默认：' + v.default));
        remix.appendChild(item);
      });
    }
    document.querySelectorAll('.prompt-tabs .tab').forEach(t => t.classList.toggle('is-active', t.dataset.lang === state.modalLang));
    $('caseModal').hidden = false;
    document.body.style.overflow = 'hidden';
  }

  function renderModalPrompt() {
    const c = state.modalCase;
    if (!c) return;
    const text = state.modalLang === 'zh' ? (c.prompt_zh || '（暂无中文提示词）') : (c.prompt_en || c.prompt_zh || '（暂无英文提示词，可直接使用中文）');
    $('modalPrompt').textContent = text;
    document.querySelectorAll('.prompt-tabs .tab').forEach(t => t.classList.toggle('is-active', t.dataset.lang === state.modalLang));
  }

  function closeModal() {
    $('caseModal').hidden = true;
    document.body.style.overflow = '';
  }

  // ---------- Create ----------
  function fileToUpload(file) {
    return new Promise((resolve, reject) => {
      if (!file) return reject(new Error('没有选择文件'));
      if (file.size > 20 * 1024 * 1024) return reject(new Error('图片超过 20 MB'));
      if (!/^image\/(jpeg|png)$/.test(file.type)) return reject(new Error('只支持 JPG / PNG'));
      const r = new FileReader();
      r.onload = () => resolve({name: file.name, mime: file.type, data: String(r.result)});
      r.onerror = () => reject(new Error('读取文件失败'));
      r.readAsDataURL(file);
    });
  }

  function showDropImage(previewId, emptyId, upload) {
    const img = $(previewId);
    img.src = upload.data;
    img.hidden = false;
    $(emptyId).hidden = true;
  }

  async function pickPhoto(file) {
    try {
      const up = await fileToUpload(file);
      state.photo = up;
      showDropImage('photoPreview', 'photoEmpty', up);
      $('photoMeta').hidden = false;
      $('photoName').textContent = up.name;
    } catch (e) { toast(e.message, true); }
  }

  async function generate() {
    if (!state.photo) return toast('先选择一张照片', true);
    const prompt = $('promptInput').value.trim();
    if (!prompt) return toast('先写英文提示词', true);
    const btn = $('generateBtn');
    btn.disabled = true;
    const old = btn.textContent;
    btn.textContent = '生成中…（请勿重复点击）';
    setHint('generateHint', '正在请求 gen.002m.com，勿重复提交', '');
    try {
      const data = await api('/api/gen', {
        method: 'POST',
        body: JSON.stringify({prompt, photo: state.photo}),
      });
      currentStatus(data.status);
      renderCreateResult(data.entry);
      setHint('generateHint', '已生成并保存，扣 1 额度', 'ok');
    } catch (e) {
      setHint('generateHint', e.message, 'error');
      toast(e.message, true);
    } finally {
      btn.disabled = false;
      btn.textContent = old;
    }
  }

  function resultCard(entry, sourceLabel) {
    const card = el('div', 'result-card');
    const img = el('img');
    img.src = withToken('/media/' + encodeURIComponent(entry.id));
    img.alt = sourceLabel;
    const info = el('div', 'result-info');
    info.appendChild(el('h3', '', sourceLabel + ' · ' + formatTime(entry.created_at)));
    info.appendChild(el('div', 'muted', (entry.width || '?') + ' × ' + (entry.height || '?') + ' · ' + (entry.size || '')));
    const p = el('p', '', shortText(entry.prompt, 180));
    info.appendChild(p);
    const actions = el('div', 'result-actions');
    const open = el('button', 'ghost-btn', '打开文件夹');
    open.onclick = () => api('/api/open', {method: 'POST', body: JSON.stringify({kind: 'file', id: entry.id})}).catch(e => toast(e.message, true));
    const hist = el('button', 'ghost-btn', '查看历史');
    hist.onclick = () => { switchView('history'); loadHistory(); };
    actions.appendChild(open);
    actions.appendChild(hist);
    info.appendChild(actions);
    card.appendChild(img);
    card.appendChild(info);
    return card;
  }

  function renderCreateResult(entry) {
    const box = $('createResult');
    box.textContent = '';
    box.appendChild(resultCard(entry, '照片生图'));
  }

  // ---------- Faceswap ----------
  async function pickAvatar(file) {
    try {
      const up = await fileToUpload(file);
      state.avatar = up;
      showDropImage('avatarPreview', 'avatarEmpty', up);
      $('avatarMeta').hidden = false;
      $('avatarName').textContent = up.name;
    } catch (e) { toast(e.message, true); }
  }

  async function addRefs(files) {
    const room = 12 - state.refs.length;
    if (room <= 0) return toast('最多 12 张参考图', true);
    const picked = Array.from(files).slice(0, room);
    for (const f of picked) {
      try {
        const up = await fileToUpload(f);
        state.refs.push(up);
      } catch (e) { toast(f.name + '：' + e.message, true); }
    }
    renderRefs();
  }

  function renderRefs() {
    const strip = $('refStrip');
    strip.textContent = '';
    state.refs.forEach((r, i) => {
      const box = el('div', 'ref-thumb');
      const img = el('img');
      img.src = r.data;
      img.alt = r.name;
      const del = el('button', '', '×');
      del.title = '移除';
      del.onclick = () => { state.refs.splice(i, 1); renderRefs(); };
      box.appendChild(img);
      box.appendChild(del);
      strip.appendChild(box);
    });
    $('refCount').textContent = state.refs.length + ' / 12';
  }

  async function startFaceswap() {
    if (!state.avatar) return toast('先选择头像', true);
    if (!state.refs.length) return toast('至少选择 1 张参考图', true);
    const btn = $('faceswapBtn');
    btn.disabled = true;
    const old = btn.textContent;
    btn.textContent = '换脸中…';
    setHint('faceswapHint', '正在逐张生成，失败一张会跳过继续', '');
    try {
      const data = await api('/api/faceswap', {
        method: 'POST',
        body: JSON.stringify({avatar: state.avatar, refs: state.refs}),
      });
      currentStatus(data.status);
      renderFaceswapResult(data);
      setHint('faceswapHint', '完成：' + data.generated + ' / ' + data.requested + ' 张', data.generated ? 'ok' : 'error');
    } catch (e) {
      setHint('faceswapHint', e.message, 'error');
      toast(e.message, true);
    } finally {
      btn.disabled = false;
      btn.textContent = old;
    }
  }

  function renderFaceswapResult(data) {
    const box = $('faceswapResult');
    box.textContent = '';
    const grid = el('div', 'ref-strip');
    grid.style.gap = '12px';
    let shown = 0;
    (data.results || []).forEach((r, i) => {
      if (r.entry) {
        const wrap = el('div');
        wrap.style.width = '210px';
        wrap.appendChild(resultCard(r.entry, '换脸 #' + (i + 1)));
        grid.appendChild(wrap);
        shown++;
      } else {
        const fail = el('div', 'remix-item', '第 ' + (i + 1) + ' 张：' + (r.error || '失败'));
        fail.style.width = '100%';
        box.appendChild(fail);
      }
    });
    if (shown) box.appendChild(grid);
    if (!shown && !(data.results || []).some(r => !r.entry)) {
      box.appendChild(el('div', 'result-placeholder', '没有生成结果'));
    }
  }

  // ---------- History ----------
  async function loadHistory() {
    const params = new URLSearchParams();
    if (state.historyQuery) params.set('q', state.historyQuery);
    if (state.historySource) params.set('source', state.historySource);
    if (state.historyStarred) params.set('starred', '1');
    try {
      const data = await api('/api/history?' + params.toString());
      state.history = data.items || [];
      renderHistory();
    } catch (e) { toast(e.message, true); }
  }

  function renderHistory() {
    const grid = $('historyGrid');
    grid.textContent = '';
    const frag = document.createDocumentFragment();
    state.history.forEach(e => frag.appendChild(historyCard(e)));
    grid.appendChild(frag);
    $('historyEmpty').hidden = state.history.length > 0;
    $('historyCountBadge').textContent = state.history.length + ' 条';
    $('navHistoryCount').textContent = state.history.length;
  }

  function historyCard(e) {
    const card = el('div', 'history-card');
    const media = el('div', 'history-media');
    const img = el('img');
    img.loading = 'lazy';
    img.src = withToken('/media/' + encodeURIComponent(e.id));
    img.alt = e.source === 'faceswap' ? '换脸成图' : '生图成图';
    img.title = '打开所在文件夹';
    img.onclick = () => api('/api/open', {method: 'POST', body: JSON.stringify({kind: 'file', id: e.id})}).catch(err => toast(err.message, true));
    media.appendChild(img);
    const body = el('div', 'history-body');
    const meta = el('div', 'meta');
    meta.appendChild(el('span', '', e.source === 'faceswap' ? '换脸套图' : '照片生图'));
    meta.appendChild(el('span', '', formatTime(e.created_at)));
    body.appendChild(meta);
    body.appendChild(el('p', '', shortText(e.prompt, 80)));
    const acts = el('div', 'history-actions');
    const star = el('button', 'star' + (e.starred ? ' is-on' : ''), e.starred ? '★ 星标' : '☆ 星标');
    star.onclick = async () => {
      try {
        const r = await api('/api/history/star', {method: 'POST', body: JSON.stringify({id: e.id})});
        e.starred = r.starred;
        renderHistory();
      } catch (err) { toast(err.message, true); }
    };
    const open = el('button', '', '打开');
    open.onclick = () => api('/api/open', {method: 'POST', body: JSON.stringify({kind: 'file', id: e.id})}).catch(err => toast(err.message, true));
    const del = el('button', '', '删除');
    del.onclick = async () => {
      if (!confirm('删除这条历史与本地成图？此操作不可撤销。')) return;
      try {
        await api('/api/history/delete', {method: 'POST', body: JSON.stringify({id: e.id})});
        state.history = state.history.filter(x => x.id !== e.id);
        renderHistory();
        toast('已删除');
      } catch (err) { toast(err.message, true); }
    };
    acts.appendChild(star); acts.appendChild(open); acts.appendChild(del);
    body.appendChild(acts);
    card.appendChild(media);
    card.appendChild(body);
    return card;
  }

  async function exportHistory() {
    try {
      const data = await api('/api/history/export', {method: 'POST', body: '{}'});
      toast('已导出 ' + data.count + ' 条到：' + data.path);
      setTimeout(() => api('/api/open', {method: 'POST', body: JSON.stringify({kind: 'exports'})}).catch(() => {}), 300);
    } catch (e) { toast(e.message, true); }
  }

  // ---------- Settings ----------
  function fillSettings(boot) {
    $('deviceId').textContent = boot.device_id || '—';
    $('dataDir').textContent = boot.data_dir || '—';
    const s = boot.settings || {};
    document.querySelectorAll('input[name=proxyMode]').forEach(r => { r.checked = r.value === (s.proxy_mode || 'system'); });
    $('proxyURL').value = s.proxy_url || '';
  }

  async function saveProxy() {
    const mode = document.querySelector('input[name=proxyMode]:checked')?.value || 'system';
    const proxy_url = $('proxyURL').value.trim();
    try {
      const data = await api('/api/settings', {method: 'POST', body: JSON.stringify({proxy_mode: mode, proxy_url})});
      setHint('proxyHint', '已保存：' + data.settings.proxy_mode, 'ok');
      toast('代理设置已保存');
    } catch (e) {
      setHint('proxyHint', e.message, 'error');
      toast(e.message, true);
    }
  }

  async function redeem() {
    const code = $('redeemCode').value.trim();
    if (!code) return toast('请输入兑换码', true);
    const btn = $('redeemBtn');
    btn.disabled = true;
    try {
      const data = await api('/api/redeem', {method: 'POST', body: JSON.stringify({code})});
      currentStatus(data.status);
      $('redeemCode').value = '';
      setHint('redeemHint', '兑换成功，+' + data.added + ' 张', 'ok');
      toast('兑换成功，+' + data.added + ' 张');
    } catch (e) {
      setHint('redeemHint', e.message, 'error');
      toast(e.message, true);
    } finally { btn.disabled = false; }
  }

  async function checkUpdate() {
    try {
      const data = await api('/api/update');
      $('latestVersion').textContent = data.latest || '—';
      setHint('updateHint', data.note || '', data.available ? 'ok' : '');
      toast(data.note || '检查完成');
    } catch (e) { toast(e.message, true); }
  }

  async function refreshStatus() {
    try {
      const data = await api('/api/status');
      currentStatus(data.status);
      toast('额度已刷新');
    } catch (e) { toast(e.message, true); }
  }

  async function refreshAll() {
    try {
      const boot = await api('/api/bootstrap');
      state.boot = boot;
      state.cases = boot.cases.cases || [];
      currentStatus(boot.status);
      fillSettings(boot);
      if (boot.status_error) setHint('proxyHint', boot.status_error, 'error');
      renderCategoryChips();
      renderTagFilter();
      renderGallery();
      state.history = boot.history || [];
      renderHistory();
      toast('已刷新');
    } catch (e) { toast(e.message, true); }
  }

  function bindDrop(zoneId, inputId, handler, multiple = false) {
    const zone = $(zoneId);
    const input = $(inputId);
    zone.onclick = () => input.click();
    zone.onkeydown = (e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); input.click(); } };
    input.onchange = () => { if (input.files && input.files.length) handler(multiple ? input.files : input.files[0]); input.value = ''; };
    zone.ondragover = (e) => { e.preventDefault(); zone.classList.add('is-drag'); };
    zone.ondragleave = () => zone.classList.remove('is-drag');
    zone.ondrop = (e) => {
      e.preventDefault();
      zone.classList.remove('is-drag');
      if (e.dataTransfer.files && e.dataTransfer.files.length) handler(multiple ? e.dataTransfer.files : e.dataTransfer.files[0]);
    };
  }

  function initEvents() {
    document.querySelectorAll('.nav-item').forEach(b => b.onclick = () => switchView(b.dataset.view));
    $('refreshBtn').onclick = refreshAll;

    let searchTimer;
    $('caseSearch').oninput = (e) => {
      state.query = e.target.value;
      clearTimeout(searchTimer);
      searchTimer = setTimeout(renderGallery, 120);
    };
    $('caseSearchClear').onclick = () => { state.query = ''; renderGallery(); };
    $('caseReset').onclick = () => {
      state.query = ''; state.category = 'all'; state.tag = '';
      $('tagFilter').value = '';
      renderGallery();
    };

    bindDrop('photoDrop', 'photoInput', pickPhoto);
    $('photoRemove').onclick = () => { state.photo = null; $('photoPreview').hidden = true; $('photoPreview').src = ''; $('photoEmpty').hidden = false; $('photoMeta').hidden = true; };
    $('promptInput').oninput = (e) => { $('promptCount').textContent = e.target.value.length + ' / 5000'; };
    $('generateBtn').onclick = generate;

    bindDrop('avatarDrop', 'avatarInput', pickAvatar);
    bindDrop('refDrop', 'refInput', addRefs, true);
    $('avatarRemove').onclick = () => { state.avatar = null; $('avatarPreview').hidden = true; $('avatarPreview').src = ''; $('avatarEmpty').hidden = false; $('avatarMeta').hidden = true; };
    $('faceswapBtn').onclick = startFaceswap;
    $('faceswapClear').onclick = () => { state.avatar = null; state.refs = []; $('avatarPreview').hidden = true; $('avatarPreview').src = ''; $('avatarEmpty').hidden = false; $('avatarMeta').hidden = true; renderRefs(); };

    let histTimer;
    $('historySearch').oninput = (e) => { state.historyQuery = e.target.value; clearTimeout(histTimer); histTimer = setTimeout(loadHistory, 180); };
    $('historySource').onchange = (e) => { state.historySource = e.target.value; loadHistory(); };
    $('historyStarred').onchange = (e) => { state.historyStarred = e.target.checked; loadHistory(); };
    $('exportBtn').onclick = exportHistory;
    $('openWorksBtn').onclick = () => api('/api/open', {method: 'POST', body: JSON.stringify({kind: 'works'})}).catch(e => toast(e.message, true));

    $('saveProxy').onclick = saveProxy;
    $('redeemBtn').onclick = redeem;
    $('checkUpdate').onclick = checkUpdate;
    $('refreshStatus').onclick = refreshStatus;
    $('copyDevice').onclick = () => navigator.clipboard.writeText($('deviceId').textContent).then(() => toast('设备 ID 已复制')).catch(() => toast('复制失败', true));
    $('openData').onclick = () => api('/api/open', {method: 'POST', body: JSON.stringify({kind: 'data'})}).catch(e => toast(e.message, true));

    $('modalClose').onclick = closeModal;
    $('caseModal').querySelector('[data-close]').onclick = closeModal;
    document.querySelectorAll('.prompt-tabs .tab').forEach(t => t.onclick = () => { state.modalLang = t.dataset.lang; renderModalPrompt(); });
    $('usePromptBtn').onclick = () => {
      const c = state.modalCase;
      if (!c) return;
      const text = state.modalLang === 'zh' ? (c.prompt_zh || '') : (c.prompt_en || c.prompt_zh || '');
      $('promptInput').value = text;
      $('promptCount').textContent = text.length + ' / 5000';
      closeModal();
      switchView('create');
      toast('已把提示词填入「照片生图」');
    };
    document.addEventListener('keydown', (e) => { if (e.key === 'Escape' && !$('caseModal').hidden) closeModal(); });
  }

  async function init() {
    initEvents();
    switchView('gallery');
    try {
      const boot = await api('/api/bootstrap');
      state.boot = boot;
      state.cases = boot.cases.cases || [];
      currentStatus(boot.status);
      fillSettings(boot);
      renderCategoryChips();
      renderTagFilter();
      renderGallery();
      state.history = boot.history || [];
      renderHistory();
      if (boot.status_error) {
        $('quotaText').textContent = '额度查询失败';
        toast(boot.status_error, true);
      }
    } catch (e) {
      toast('初始化失败：' + e.message, true);
      $('quotaText').textContent = '服务未连接';
    }
  }

  document.addEventListener('DOMContentLoaded', init);
})();

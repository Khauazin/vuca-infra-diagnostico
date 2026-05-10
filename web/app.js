(function () {
  const $ = (id) => document.getElementById(id);

  const STORAGE_KEY = 'vuca-diag-config';

  function adicionarImpressora(imp = { nome: '', ip: '', porta: 9100 }) {
    const wrap = document.createElement('div');
    wrap.className = 'impressora-row';
    wrap.innerHTML = `
      <label><span>Nome</span><input type="text" class="js-imp-nome" placeholder="Cozinha" value="${imp.nome || ''}" /></label>
      <label><span>IP</span><input type="text" class="js-imp-ip" placeholder="192.168.0.100" value="${imp.ip || ''}" /></label>
      <label><span>Porta</span><input type="number" class="js-imp-porta" value="${imp.porta || 9100}" /></label>
      <button type="button" class="btn btn--ghost btn--icon js-imp-remover" title="Remover">&times;</button>
    `;
    wrap.querySelector('.js-imp-remover').addEventListener('click', () => wrap.remove());
    $('js-impressoras').appendChild(wrap);
  }

  function lerConfig() {
    const portasStr = $('rabbit_portas').value;
    const portas = portasStr.split(',').map(s => parseInt(s.trim(), 10)).filter(n => !isNaN(n));
    const impressoras = [...document.querySelectorAll('.impressora-row')].map(row => ({
      nome: row.querySelector('.js-imp-nome').value.trim(),
      ip: row.querySelector('.js-imp-ip').value.trim(),
      porta: parseInt(row.querySelector('.js-imp-porta').value, 10) || 9100,
    })).filter(i => i.ip);

    return {
      instancia: $('instancia').value.trim(),
      vucalocal: $('vucalocal').value.trim(),
      rabbitmq: { host: $('rabbit_host').value.trim() || 'localhost', portas },
      impressoras,
    };
  }

  function salvarConfig(cfg) {
    try { localStorage.setItem(STORAGE_KEY, JSON.stringify(cfg)); } catch (e) {}
  }

  function carregarConfig() {
    try {
      const raw = localStorage.getItem(STORAGE_KEY);
      if (!raw) return;
      const cfg = JSON.parse(raw);
      if (cfg.instancia) $('instancia').value = cfg.instancia;
      if (cfg.vucalocal) $('vucalocal').value = cfg.vucalocal;
      if (cfg.rabbitmq) {
        if (cfg.rabbitmq.host) $('rabbit_host').value = cfg.rabbitmq.host;
        if (Array.isArray(cfg.rabbitmq.portas)) $('rabbit_portas').value = cfg.rabbitmq.portas.join(', ');
      }
      if (Array.isArray(cfg.impressoras) && cfg.impressoras.length) {
        $('js-impressoras').innerHTML = '';
        cfg.impressoras.forEach(adicionarImpressora);
      }
    } catch (e) {}
  }

  function statusClass(status) {
    return ({ ok: 'dot--ok', warn: 'dot--warn', fail: 'dot--fail', info: 'dot--info' }[status] || 'dot--info');
  }

  function renderProgresso(res) {
    const li = document.createElement('li');
    li.innerHTML = `<span class="dot ${statusClass(res.status)}"></span><strong>${res.categoria} — ${res.nome}</strong><span style="color:#9ca3af; margin-left:auto;">${res.duracao_ms}ms</span>`;
    $('js-progresso-lista').appendChild(li);
  }

  function renderRelatorio(rel) {
    const cont = { ok: 0, warn: 0, fail: 0, info: 0 };
    rel.resultados.forEach(r => { cont[r.status] = (cont[r.status] || 0) + 1; });

    $('js-resumo').innerHTML = `
      <div class="resumo-card resumo-card--ok"><div class="resumo-card__valor">${cont.ok || 0}</div><div class="resumo-card__label">OK</div></div>
      <div class="resumo-card resumo-card--warn"><div class="resumo-card__valor">${cont.warn || 0}</div><div class="resumo-card__label">Atencao</div></div>
      <div class="resumo-card resumo-card--fail"><div class="resumo-card__valor">${cont.fail || 0}</div><div class="resumo-card__label">Falhas</div></div>
      <div class="resumo-card"><div class="resumo-card__valor">${rel.resultados.length}</div><div class="resumo-card__label">Total</div></div>
    `;

    $('js-detalhes').innerHTML = rel.resultados.map(r => `
      <div class="check-item">
        <div class="check-item__head">
          <span class="dot ${statusClass(r.status)}"></span>
          <div class="check-item__title">
            <div class="check-item__categoria">${r.categoria}</div>
            <div class="check-item__nome">${r.nome}</div>
            <div class="check-item__msg">${r.mensagem}</div>
          </div>
          <span class="check-item__duracao">${r.duracao_ms}ms</span>
        </div>
        <div class="check-item__detalhes"><pre>${JSON.stringify(r.detalhes || {}, null, 2)}</pre></div>
      </div>
    `).join('');

    document.querySelectorAll('.check-item__head').forEach(h => {
      h.addEventListener('click', () => h.parentElement.classList.toggle('aberto'));
    });
  }

  async function executar(cfg) {
    $('js-progresso').classList.remove('hidden');
    $('js-progresso-lista').innerHTML = '';
    $('js-resultado').classList.add('hidden');
    $('js-executar').disabled = true;

    const resp = await fetch('/api/diagnosticar', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(cfg),
    });

    if (!resp.ok || !resp.body) {
      alert('Falha ao iniciar diagnostico');
      $('js-executar').disabled = false;
      return;
    }

    const reader = resp.body.getReader();
    const decoder = new TextDecoder();
    let buffer = '';
    let relatorio = null;

    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      const linhas = buffer.split('\n');
      buffer = linhas.pop() || '';
      for (const linha of linhas) {
        if (!linha.trim()) continue;
        try {
          const evt = JSON.parse(linha);
          if (evt.tipo === 'resultado') renderProgresso(evt.dados);
          else if (evt.tipo === 'final') relatorio = evt.dados;
        } catch (e) {}
      }
    }

    if (relatorio) {
      $('js-progresso').classList.add('hidden');
      $('js-resultado').classList.remove('hidden');
      window.__relatorio = relatorio;
      renderRelatorio(relatorio);
    }
    $('js-executar').disabled = false;
  }

  async function baixar() {
    if (!window.__relatorio) return;
    const resp = await fetch('/api/relatorio.html', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(window.__relatorio),
    });
    const blob = await resp.blob();
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    const d = new Date();
    const pad = (n) => String(n).padStart(2, '0');
    const stamp = `${d.getFullYear()}-${pad(d.getMonth()+1)}-${pad(d.getDate())}-${pad(d.getHours())}${pad(d.getMinutes())}`;
    a.href = url;
    a.download = `relatorio-${stamp}.html`;
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  }

  document.addEventListener('DOMContentLoaded', () => {
    $('js-add-impressora').addEventListener('click', () => adicionarImpressora());
    $('instancia').addEventListener('input', (e) => {
      const v = e.target.value.trim() || '{instancia}';
      $('js-host-preview').textContent = `${v}.vucasolution.com.br`;
    });

    $('js-form').addEventListener('submit', (e) => {
      e.preventDefault();
      const cfg = lerConfig();
      if (!cfg.instancia) { alert('Informe a instancia'); return; }
      salvarConfig(cfg);
      executar(cfg);
    });

    $('js-baixar').addEventListener('click', baixar);
    $('js-novo').addEventListener('click', () => {
      $('js-resultado').classList.add('hidden');
      $('js-progresso').classList.add('hidden');
      window.scrollTo({ top: 0, behavior: 'smooth' });
    });

    carregarConfig();
    if (!document.querySelector('.impressora-row')) adicionarImpressora();
  });
})();

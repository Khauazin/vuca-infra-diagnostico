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

  function adicionarPorta(tp = { nome: '', host: '', porta: 8080 }) {
    const wrap = document.createElement('div');
    wrap.className = 'impressora-row';
    wrap.innerHTML = `
      <label><span>Nome</span><input type="text" class="js-porta-nome" placeholder="Postgres" value="${tp.nome || ''}" /></label>
      <label><span>Host</span><input type="text" class="js-porta-host" placeholder="192.168.0.100" value="${tp.host || ''}" /></label>
      <label><span>Porta</span><input type="number" class="js-porta-porta" value="${tp.porta || 8080}" /></label>
      <button type="button" class="btn btn--ghost btn--icon js-porta-remover" title="Remover">&times;</button>
    `;
    wrap.querySelector('.js-porta-remover').addEventListener('click', () => wrap.remove());
    $('js-portas-customizadas').appendChild(wrap);
  }

  function lerConfig() {
    const portasStr = $('rabbit_portas').value;
    const portas = portasStr.split(',').map(s => parseInt(s.trim(), 10)).filter(n => !isNaN(n));
    const impressoras = [...document.querySelectorAll('#js-impressoras .impressora-row')].map(row => ({
      nome: row.querySelector('.js-imp-nome').value.trim(),
      ip: row.querySelector('.js-imp-ip').value.trim(),
      porta: parseInt(row.querySelector('.js-imp-porta').value, 10) || 9100,
    })).filter(i => i.ip);
    const portasCustomizadas = [...document.querySelectorAll('#js-portas-customizadas .impressora-row')].map(row => ({
      nome: row.querySelector('.js-porta-nome').value.trim(),
      host: row.querySelector('.js-porta-host').value.trim(),
      porta: parseInt(row.querySelector('.js-porta-porta').value, 10) || 0,
    })).filter(p => p.host && p.porta > 0);

    return {
      instancia: $('instancia').value.trim(),
      vucalocal: $('vucalocal').value.trim(),
      rabbitmq: { host: $('rabbit_host').value.trim() || 'localhost', portas },
      impressoras,
      portas_customizadas: portasCustomizadas,
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
      if (Array.isArray(cfg.portas_customizadas) && cfg.portas_customizadas.length) {
        $('js-portas-customizadas').innerHTML = '';
        cfg.portas_customizadas.forEach(adicionarPorta);
      }
    } catch (e) {}
  }

  function statusClass(status) {
    return ({ ok: 'dot--ok', warn: 'dot--warn', fail: 'dot--fail', info: 'dot--info' }[status] || 'dot--info');
  }

  function escapeHtml(s) {
    return String(s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  }

  function chaveCheck(categoria, nome) { return `${categoria}//${nome}`; }

  function bindCardEvents(card) {
    if (card._bound) return;
    card._bound = true;
    card.querySelector('.check-item__head').addEventListener('click', () => {
      card.classList.toggle('aberto');
    });
    card.querySelectorAll('.aba').forEach(btn => {
      btn.addEventListener('click', (e) => {
        e.stopPropagation();
        const det = btn.closest('.check-item__detalhes');
        const alvo = btn.dataset.aba;
        det.querySelectorAll('.aba').forEach(b => b.classList.toggle('aba--ativa', b.dataset.aba === alvo));
        det.querySelectorAll('.aba-conteudo').forEach(c => c.classList.toggle('aba-conteudo--ativa', c.dataset.conteudo === alvo));
      });
    });
  }

  function criarCardExecutando(categoria, nome) {
    const card = document.createElement('div');
    card.className = 'check-item check-item--executando';
    card.dataset.categoria = categoria;
    card.dataset.nome = nome;
    card.innerHTML = `
      <div class="check-item__head">
        <span class="dot dot--executando"></span>
        <div class="check-item__title">
          <div class="check-item__categoria">${escapeHtml(categoria)}</div>
          <div class="check-item__nome">${escapeHtml(nome)}</div>
          <div class="check-item__msg">Executando...</div>
        </div>
        <span class="check-item__badge">EXECUTANDO</span>
      </div>
      <div class="check-item__detalhes">
        <div class="abas" role="tablist">
          <button type="button" class="aba" data-aba="mensagem">Mensagem</button>
          <button type="button" class="aba aba--ativa" data-aba="etapas">Etapas (0)</button>
          <button type="button" class="aba" data-aba="json">Detalhes</button>
        </div>
        <div class="aba-conteudo" data-conteudo="mensagem">
          <p class="aba-mensagem aba-mensagem--info">Em execucao... aguarde o check terminar.</p>
        </div>
        <div class="aba-conteudo aba-conteudo--ativa" data-conteudo="etapas">
          <ol class="etapas-lista"></ol>
        </div>
        <div class="aba-conteudo" data-conteudo="json">
          <pre>(aguardando resultado)</pre>
        </div>
      </div>
    `;
    bindCardEvents(card);
    return card;
  }

  function adicionarSubpassoAoCard(card, sp) {
    const lista = card.querySelector('.etapas-lista');
    const idx = lista.children.length + 1;
    const li = document.createElement('li');
    li.className = `etapa-item etapa-item--${statusClass(sp.status).replace('dot--', '')}`;
    li.innerHTML = `
      <div class="etapa-item__head">
        <span class="etapa-item__numero">${idx}</span>
        <span class="dot ${statusClass(sp.status)}"></span>
        <span class="etapa-item__descricao">${escapeHtml(sp.descricao || '')}</span>
        ${sp.duracao_ms ? `<span class="etapa-item__duracao">${sp.duracao_ms}ms</span>` : ''}
      </div>
      ${sp.detalhe ? `<div class="etapa-item__detalhe">${escapeHtml(sp.detalhe)}</div>` : ''}
    `;
    lista.appendChild(li);
    const tabBtn = card.querySelector('.aba[data-aba="etapas"]');
    if (tabBtn) tabBtn.textContent = `Etapas (${lista.children.length})`;
  }

  function finalizarCard(card, r) {
    card.classList.remove('check-item--executando');

    const dot = card.querySelector('.check-item__head > .dot');
    if (dot) dot.className = `dot ${statusClass(r.status)}`;

    const msg = card.querySelector('.check-item__msg');
    if (msg) msg.textContent = r.mensagem || '';

    const badge = card.querySelector('.check-item__badge');
    if (badge) badge.remove();

    const head = card.querySelector('.check-item__head');
    let dur = card.querySelector('.check-item__duracao');
    if (!dur) {
      dur = document.createElement('span');
      dur.className = 'check-item__duracao';
      head.appendChild(dur);
    }
    dur.textContent = `${r.duracao_ms || 0}ms`;

    const abaMsg = card.querySelector('.aba-conteudo[data-conteudo="mensagem"] .aba-mensagem');
    if (abaMsg) {
      abaMsg.className = `aba-mensagem aba-mensagem--${statusClass(r.status).replace('dot--', '')}`;
      abaMsg.textContent = r.mensagem || 'Sem mensagem';
    }

    const pre = card.querySelector('.aba-conteudo[data-conteudo="json"] pre');
    if (pre) pre.textContent = JSON.stringify(r.detalhes || {}, null, 2);

    // Se nao houver sub-passos (sentinelas como "Validacao parcial"), mostra placeholder
    const lista = card.querySelector('.etapas-lista');
    if (lista && lista.children.length === 0) {
      const conteudoEtapas = card.querySelector('.aba-conteudo[data-conteudo="etapas"]');
      if (conteudoEtapas) {
        conteudoEtapas.innerHTML = `<p class="etapas-vazio">Este check nao expoe sub-passos.</p>`;
      }
      const tabBtn = card.querySelector('.aba[data-aba="etapas"]');
      if (tabBtn) tabBtn.textContent = `Etapas`;
    }

    // Apos finalizar, troca para aba "Mensagem" automaticamente
    card.querySelectorAll('.aba').forEach(b => b.classList.toggle('aba--ativa', b.dataset.aba === 'mensagem'));
    card.querySelectorAll('.aba-conteudo').forEach(c => c.classList.toggle('aba-conteudo--ativa', c.dataset.conteudo === 'mensagem'));
  }

  function obterOuCriarCard(categoria, nome, cardsAtivos) {
    const key = chaveCheck(categoria, nome);
    let card = cardsAtivos.get(key);
    if (!card) {
      card = criarCardExecutando(categoria, nome);
      $('js-detalhes').appendChild(card);
      cardsAtivos.set(key, card);
    }
    return card;
  }

  function atualizarResumo(rel) {
    const cont = { ok: 0, warn: 0, fail: 0, info: 0 };
    rel.resultados.forEach(r => { cont[r.status] = (cont[r.status] || 0) + 1; });
    $('js-resumo').innerHTML = `
      <div class="resumo-card resumo-card--ok"><div class="resumo-card__valor">${cont.ok || 0}</div><div class="resumo-card__label">OK</div></div>
      <div class="resumo-card resumo-card--warn"><div class="resumo-card__valor">${cont.warn || 0}</div><div class="resumo-card__label">Atencao</div></div>
      <div class="resumo-card resumo-card--fail"><div class="resumo-card__valor">${cont.fail || 0}</div><div class="resumo-card__label">Falhas</div></div>
      <div class="resumo-card"><div class="resumo-card__valor">${rel.resultados.length}</div><div class="resumo-card__label">Total</div></div>
    `;
  }

  function encontrarResultado(rel, categoria, nome) {
    if (!rel || !Array.isArray(rel.resultados)) return null;
    return rel.resultados.find(r => r.categoria === categoria && r.nome === nome) || null;
  }

  function mostrarAlertaErro(rel) {
    const interrompido = encontrarResultado(rel, 'Validacao', 'Diagnostico interrompido');
    if (!interrompido) return false;

    const dns = encontrarResultado(rel, 'Conectividade', 'Resolucao DNS');
    const https = encontrarResultado(rel, 'Conectividade', 'HTTPS');

    const instancia = (rel.config && rel.config.instancia) ? rel.config.instancia : '';
    const resumo = instancia
      ? `A instancia "${escapeHtml(instancia)}" nao foi validada — o cluster nao tem rota para esse nome.`
      : 'A validacao da instancia falhou.';
    $('js-alerta-resumo').innerHTML = resumo;

    const linhas = [];
    if (dns) {
      linhas.push(`<li><strong>DNS:</strong> ${escapeHtml(dns.mensagem || '')}</li>`);
    }
    if (https) {
      const det = https.detalhes || {};
      let extra = '';
      if (det.body_snippet) {
        extra = ` <code class="alerta__codigo">${escapeHtml(String(det.body_snippet))}</code>`;
      }
      linhas.push(`<li><strong>HTTPS:</strong> ${escapeHtml(https.mensagem || '')}${extra}</li>`);
    }
    $('js-alerta-detalhes').innerHTML = linhas.length ? `<ul class="alerta__lista">${linhas.join('')}</ul>` : '';

    $('js-alerta-erro').classList.remove('hidden');
    $('js-progresso').classList.add('hidden');
    $('js-resultado').classList.add('hidden');
    return true;
  }

  async function executar(cfg) {
    $('js-alerta-erro').classList.add('hidden');
    $('js-progresso').classList.add('hidden');
    $('js-resultado').classList.remove('hidden');
    $('js-detalhes').innerHTML = '';
    $('js-resumo').innerHTML = `<div class="resumo-card"><div class="resumo-card__valor">--</div><div class="resumo-card__label">Em execucao...</div></div>`;
    $('js-executar').disabled = true;

    const cardsAtivos = new Map();

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
        let evt;
        try { evt = JSON.parse(linha); } catch (e) { continue; }

        if (evt.tipo === 'check_inicio') {
          const r = evt.dados;
          obterOuCriarCard(r.categoria, r.nome, cardsAtivos);
        } else if (evt.tipo === 'subpasso') {
          const ev = evt.dados;
          const card = obterOuCriarCard(ev.check_categoria, ev.check_nome, cardsAtivos);
          adicionarSubpassoAoCard(card, ev.subpasso);
        } else if (evt.tipo === 'resultado') {
          const r = evt.dados;
          const card = obterOuCriarCard(r.categoria, r.nome, cardsAtivos);
          finalizarCard(card, r);
        } else if (evt.tipo === 'final') {
          relatorio = evt.dados;
        }
      }
    }

    if (relatorio) {
      window.__relatorio = relatorio;
      if (mostrarAlertaErro(relatorio)) {
        $('js-resultado').classList.add('hidden');
      } else {
        atualizarResumo(relatorio);
      }
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
    $('js-add-porta').addEventListener('click', () => adicionarPorta());
    $('instancia').addEventListener('input', (e) => {
      const v = e.target.value.trim() || '{instancia}';
      $('js-host-preview').textContent = `${v}.vucasolution.com.br`;
    });

    $('js-form').addEventListener('submit', (e) => {
      e.preventDefault();
      const cfg = lerConfig();
      salvarConfig(cfg);
      executar(cfg);
    });

    $('js-baixar').addEventListener('click', baixar);
    $('js-novo').addEventListener('click', () => {
      $('js-resultado').classList.add('hidden');
      $('js-progresso').classList.add('hidden');
      $('js-alerta-erro').classList.add('hidden');
      window.scrollTo({ top: 0, behavior: 'smooth' });
    });
    $('js-alerta-fechar').addEventListener('click', () => {
      $('js-alerta-erro').classList.add('hidden');
      window.scrollTo({ top: 0, behavior: 'smooth' });
    });

    carregarConfig();
    if (!document.querySelector('.impressora-row')) adicionarImpressora();
  });
})();

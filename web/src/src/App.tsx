import type { TFunction } from 'i18next'
import { QRCodeSVG } from 'qrcode.react'
import type { ReactNode } from 'react'
import { FormEvent, memo, useCallback, useDeferredValue, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { api } from './api'
import { localeConfig, resolvedLocale } from './i18n'
import type { Config, FrontProxySettings, LocalPlan, MatrixSlot, MatrixSlotsInfo, Node, Protocol, SharePoolInfo, ShareSlot, Status, SubscriptionClientInfo, SubscriptionInfo, VersionInfo } from './types'

const protocolLabels: Record<Protocol, string> = {
  openvpn_udp: 'OpenVPN UDP',
  openvpn_tcp: 'OpenVPN TCP',
  softether_https: 'SoftEther TLS',
  sstp: 'SSTP',
  l2tp: 'L2TP/IPsec',
  socks5: 'SOCKS5',
  http: 'HTTP CONNECT',
}

type View = 'nodes' | 'subscription' | 'share' | 'settings'
type Theme = 'light' | 'dark'
type SortKey = 'speedBps' | 'pingMs' | 'score' | 'measuredBps' | 'countryShort'
type NodeViewMode = 'cards' | 'list' | 'table'

const protocolDisplayOrder: Protocol[] = ['openvpn_udp', 'openvpn_tcp', 'softether_https', 'sstp', 'l2tp', 'socks5', 'http']
const selectionSort: Record<Config['selectionMode'], { key: SortKey; direction: 'asc' | 'desc' }> = {
  speed: { key: 'speedBps', direction: 'desc' },
  ping: { key: 'pingMs', direction: 'asc' },
  score: { key: 'score', direction: 'desc' },
}

function App() {
  const { t, i18n } = useTranslation()
  const [authenticated, setAuthenticated] = useState<boolean | null>(null)
  const [version, setVersion] = useState<VersionInfo>()
  const [view, setView] = useState<View>('nodes')
  const [theme, setTheme] = useState<Theme>(preferredTheme)

  useEffect(() => {
    document.documentElement.dataset.theme = theme
    document.documentElement.style.colorScheme = theme
    window.localStorage.setItem('proxygate:theme', theme)
  }, [theme])

  useEffect(() => {
    document.documentElement.lang = resolvedLocale(i18n.resolvedLanguage)
  }, [i18n.resolvedLanguage])

  useEffect(() => {
    api.get<VersionInfo>('/api/version').then(setVersion).catch(() => undefined)
    api.get<Status>('/api/status').then(() => setAuthenticated(true)).catch(() => setAuthenticated(false))
    const unauthorized = () => setAuthenticated(false)
    window.addEventListener('proxygate:unauthorized', unauthorized)
    return () => window.removeEventListener('proxygate:unauthorized', unauthorized)
  }, [])

  if (authenticated === null) return <LoadingScreen />
  if (!authenticated) return <Login version={version} theme={theme} onToggleTheme={() => setTheme(current => current === 'dark' ? 'light' : 'dark')} onSuccess={() => setAuthenticated(true)} />

  const logout = () => api.post('/api/logout').finally(() => setAuthenticated(false))
  return <div className="app-shell">
    <header className="topbar">
      <div className="brand">
        <div className="brand-mark" aria-hidden="true"><span /></div>
        <div><BrandTitle version={version} /><small>{t('app.subtitle')}</small></div>
      </div>
      <nav className="primary-nav" role="tablist" aria-label={t('app.navigation')}>
        <button role="tab" aria-selected={view === 'nodes'} className={view === 'nodes' ? 'active' : ''} onClick={() => setView('nodes')}><span>{t('app.nodes')}</span><small>{t('app.nodes')}</small></button>
        <button role="tab" aria-selected={view === 'subscription'} className={view === 'subscription' ? 'active' : ''} onClick={() => setView('subscription')}><span>{t('app.subscription')}</span><small>{t('app.subscription')}</small></button>
        <button role="tab" aria-selected={view === 'share'} className={view === 'share' ? 'active' : ''} onClick={() => setView('share')}><span>{t('app.share')}</span><small>{t('app.share')}</small></button>
        <button role="tab" aria-selected={view === 'settings'} className={view === 'settings' ? 'active' : ''} onClick={() => setView('settings')}><span>{t('app.settings')}</span><small>{t('app.settings')}</small></button>
      </nav>
      <div className="topbar-actions">
        <LanguageSelect />
        <button className="quiet-button theme-button" title={t('theme.switch')} onClick={() => setTheme(current => current === 'dark' ? 'light' : 'dark')}><ThemeIcon theme={theme} /><span>{theme === 'dark' ? t('theme.light') : t('theme.dark')}</span></button>
        <button className="quiet-button" onClick={logout}>{t('app.logout')}</button>
      </div>
    </header>
    <main className="workspace">{view === 'nodes' ? <Nodes /> : view === 'subscription' ? <Subscription /> : view === 'share' ? <Share /> : <Settings />}</main>
  </div>
}

function LoadingScreen() {
  return <div className="loading-screen"><div className="brand-mark large" /><div className="pulse-line" /></div>
}

function Login({ version, theme, onToggleTheme, onSuccess }: { version?: VersionInfo; theme: Theme; onToggleTheme: () => void; onSuccess: () => void }) {
  const { t } = useTranslation()
  const [username, setUsername] = useState('admin')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)

  const submit = async (event: FormEvent) => {
    event.preventDefault(); setError(''); setSubmitting(true)
    try {
      await api.post('/api/login', { username, password })
      onSuccess()
    } catch (reason) {
      setError(errorMessage(reason))
    } finally {
      setSubmitting(false)
    }
  }

  return <main className="login-page">
    <div className="login-controls"><LanguageSelect /><button className="quiet-button login-theme" title={t('theme.switch')} onClick={onToggleTheme}><ThemeIcon theme={theme} /><span>{theme === 'dark' ? t('theme.lightMode') : t('theme.darkMode')}</span></button></div>
    <section className="login-intro">
      <div className="brand login-brand"><div className="brand-mark large"><span /></div><div><BrandTitle version={version} /><small>{t('app.subtitle')}</small></div></div>
      <p className="eyebrow">PROXYGATE</p>
      <h1>{t('login.title')}</h1>
      <p className="login-copy">{t('login.description')}</p>
    </section>
    <form className="login-panel" onSubmit={submit}>
      <div><p className="eyebrow">{t('login.panel')}</p><h2>{t('login.heading')}</h2><p>{t('login.prompt')}</p></div>
      {error && <Notice tone="danger">{error}</Notice>}
      <Field label={t('login.username')}><input value={username} onChange={event => setUsername(event.target.value)} autoComplete="username" autoFocus /></Field>
      <Field label={t('login.password')}><input type="password" value={password} onChange={event => setPassword(event.target.value)} autoComplete="current-password" /></Field>
      <button className="primary-button wide" disabled={submitting}>{submitting ? <><Spinner />{t('login.checking')}</> : t('login.submit')}</button>
    </form>
  </main>
}

function BrandTitle({ version }: { version?: VersionInfo }) {
  return <strong>ProxyGate{version && <span className="app-version" title={version.release}>v{version.version}</span>}</strong>
}

function Nodes() {
  const { t, i18n } = useTranslation()
  const locale = resolvedLocale(i18n.resolvedLanguage)
  const sortLabels: Record<SortKey, string> = {
    speedBps: t('sort.speedBps'),
    measuredBps: t('sort.measuredBps'),
    pingMs: t('sort.pingMs'),
    score: t('sort.score'),
    countryShort: t('sort.countryShort'),
  }
  const [status, setStatus] = useState<Status>()
  const [nodes, setNodes] = useState<Node[]>([])
  const [error, setError] = useState('')
  const [query, setQuery] = useState('')
  const [countryFilter, setCountryFilter] = useState('')
  const [protocolFilter, setProtocolFilter] = useState<Protocol | ''>('')
  const [minSpeedMbps, setMinSpeedMbps] = useState(0)
  const [maxPingMs, setMaxPingMs] = useState(0)
  const [minScore, setMinScore] = useState(0)
  const [selectedIPs, setSelectedIPs] = useState<string[]>([])
  const [batchBusy, setBatchBusy] = useState(false)
  const [batchNote, setBatchNote] = useState('')
  const [sortKey, setSortKey] = useState<SortKey>('speedBps')
  const [sortDirection, setSortDirection] = useState<'asc' | 'desc'>('desc')
  const [viewMode, setViewMode] = useState<NodeViewMode>(preferredNodeView)
  const [protocolPriority, setProtocolPriority] = useState<Protocol[]>(protocolDisplayOrder)
  const [page, setPage] = useState(1)
  const [loadingNodes, setLoadingNodes] = useState(true)
  const [reconnecting, setReconnecting] = useState(false)
  const [switchingDirect, setSwitchingDirect] = useState(false)
  const [lastSynced, setLastSynced] = useState<Date>()
  const statusRequest = useRef(false)
  const nodesRequest = useRef(false)
  const nodesReloadPending = useRef(false)
  const refreshWasRunning = useRef(false)
  const previousActiveIP = useRef<string | undefined>(undefined)
  const sortTouched = useRef(false)
  const deferredQuery = useDeferredValue(query)
  const pageSize = 24

  const loadStatus = useCallback(async () => {
    if (statusRequest.current || document.visibilityState === 'hidden') return
    statusRequest.current = true
    try {
      setStatus(await api.get<Status>('/api/status'))
      setError('')
    } catch (reason) {
      setError(errorMessage(reason))
    } finally {
      statusRequest.current = false
    }
  }, [])

  const loadNodes = useCallback(async () => {
    if (document.visibilityState === 'hidden') return
    if (nodesRequest.current) {
      nodesReloadPending.current = true
      return
    }
    nodesRequest.current = true
    try {
      do {
        nodesReloadPending.current = false
        setNodes(await api.get<Node[]>('/api/nodes?limit=240'))
        setLastSynced(new Date())
        setError('')
      } while (nodesReloadPending.current && !document.hidden)
    } catch (reason) {
      setError(errorMessage(reason))
    } finally {
      nodesRequest.current = false
      setLoadingNodes(false)
    }
  }, [])

  useEffect(() => {
    void loadStatus(); void loadNodes()
    const statusTimer = window.setInterval(loadStatus, 4_000)
    const nodesTimer = window.setInterval(loadNodes, 60_000)
    const visible = () => { if (document.visibilityState === 'visible') { void loadStatus(); void loadNodes() } }
    document.addEventListener('visibilitychange', visible)
    return () => {
      window.clearInterval(statusTimer); window.clearInterval(nodesTimer)
      document.removeEventListener('visibilitychange', visible)
    }
  }, [loadNodes, loadStatus])

  useEffect(() => {
    let mounted = true
    void api.get<Config>('/api/config').then(settings => {
      if (!mounted) return
      setProtocolPriority(settings.protocolPriority)
      if (!sortTouched.current) {
        const preferred = selectionSort[settings.selectionMode]
        setSortKey(preferred.key)
        setSortDirection(preferred.direction)
      }
    }).catch(reason => {
      if (mounted) setError(errorMessage(reason))
    })
    return () => { mounted = false }
  }, [])

  useEffect(() => {
    if (refreshWasRunning.current && status?.refreshRunning === false) void loadNodes()
    refreshWasRunning.current = Boolean(status?.refreshRunning)
  }, [status?.refreshRunning, loadNodes])

  useEffect(() => {
    const activeIP = status?.activeIp
    if (!activeIP) return
    const previous = previousActiveIP.current
    previousActiveIP.current = activeIP
    if (previous && previous !== activeIP) void loadNodes()
  }, [status?.activeIp, loadNodes])

  useEffect(() => { setPage(1) }, [deferredQuery, sortKey, sortDirection, status?.activeIp, countryFilter, protocolFilter, minSpeedMbps, maxPingMs, minScore])
  useEffect(() => { window.localStorage.setItem('proxygate:nodeView', viewMode) }, [viewMode])

  const countryOptions = useMemo(() => {
    const counts = new Map<string, number>()
    for (const node of nodes) {
      const code = (node.countryShort || '').toUpperCase()
      if (code) counts.set(code, (counts.get(code) ?? 0) + 1)
    }
    return Array.from(counts.entries()).sort((left, right) => right[1] - left[1] || left[0].localeCompare(right[0]))
  }, [nodes])

  const filterActive = countryFilter !== '' || protocolFilter !== '' || minSpeedMbps > 0 || maxPingMs > 0 || minScore > 0
  const resetFilters = () => {
    setCountryFilter(''); setProtocolFilter(''); setMinSpeedMbps(0); setMaxPingMs(0); setMinScore(0)
  }

  const selectedSet = useMemo(() => new Set(selectedIPs), [selectedIPs])
  const toggleSelected = useCallback((ip: string) => {
    setSelectedIPs(current => current.includes(ip) ? current.filter(value => value !== ip) : [...current, ip])
  }, [])

  const filteredNodes = useMemo(() => {
    const normalized = deferredQuery.trim().toLowerCase()
    const base = nodes.filter(node => {
      if (countryFilter && (node.countryShort || '').toUpperCase() !== countryFilter) return false
      if (protocolFilter && !node.protocols.includes(protocolFilter)) return false
      if (minSpeedMbps > 0 && node.speedBps < minSpeedMbps * 1_000_000) return false
      if (maxPingMs > 0 && (node.pingMs <= 0 || node.pingMs > maxPingMs)) return false
      if (minScore > 0 && node.score < minScore) return false
      return true
    })
    const result = normalized ? base.filter(node => [
      node.ip, node.hostName, node.operator, node.countryShort, node.countryLong, ...node.protocols,
    ].some(value => value.toLowerCase().includes(normalized))) : base
    const multiplier = sortDirection === 'asc' ? 1 : -1
    return [...result].sort((left, right) => {
      const leftActive = left.ip === status?.activeIp
      const rightActive = right.ip === status?.activeIp
      if (leftActive !== rightActive) return leftActive ? -1 : 1

      let comparison = 0
      switch (sortKey) {
        case 'speedBps': comparison = left.speedBps - right.speedBps; break
        case 'pingMs': comparison = normalizedPing(left.pingMs) - normalizedPing(right.pingMs); break
        case 'score': comparison = left.score - right.score; break
        case 'measuredBps': comparison = (left.measuredBps || 0) - (right.measuredBps || 0); break
        case 'countryShort': comparison = left.countryShort.localeCompare(right.countryShort, locale); break
      }
      return comparison * multiplier
    })
  }, [nodes, deferredQuery, sortKey, sortDirection, status?.activeIp, locale, countryFilter, protocolFilter, minSpeedMbps, maxPingMs, minScore])

  const pageCount = Math.max(1, Math.ceil(filteredNodes.length / pageSize))
  const currentPage = Math.min(page, pageCount)
  const visibleNodes = filteredNodes.slice((currentPage - 1) * pageSize, currentPage * pageSize)
  const connected = status?.state === 'connected' && !status?.directMode
  const directMode = Boolean(status?.directMode)

  const refresh = async () => {
    try {
      await api.post('/api/refresh')
      setStatus(current => current ? { ...current, refreshRunning: true } : current)
      await loadStatus()
    } catch (reason) {
      setError(errorMessage(reason))
    }
  }

  const reconnect = async () => {
    setError(''); setReconnecting(true)
    try {
      await api.post('/api/nodes/reconnect')
      setStatus(current => current ? { ...current, state: 'connecting', connectingIp: current.activeIp } : current)
      await loadStatus()
    } catch (reason) {
      setError(errorMessage(reason))
    } finally {
      setReconnecting(false)
    }
  }

  const toggleDirect = async () => {
    if (!status) return
    setError(''); setSwitchingDirect(true)
    const enabled = !status.connectionPaused
    try {
      await api.post('/api/connection/direct', { enabled })
      setStatus(current => current ? enabled
        ? { ...current, state: 'direct', directMode: true, connectionPaused: true, activeIp: undefined, activeHostName: undefined, activeProtocol: undefined, connectingIp: undefined }
        : { ...current, state: 'connecting', connectionPaused: false }
        : current)
      await loadStatus()
    } catch (reason) {
      setError(errorMessage(reason))
    } finally {
      setSwitchingDirect(false)
    }
  }

  const selectAllFiltered = () => setSelectedIPs(filteredNodes.map(node => node.ip))
  const allFilteredSelected = filteredNodes.length > 0 && filteredNodes.every(node => selectedSet.has(node.ip))

  const batchTest = async () => {
    if (selectedIPs.length === 0) return
    setBatchBusy(true); setBatchNote('')
    let queued = 0
    for (const ip of selectedIPs) {
      try {
        await api.post(`/api/nodes/${encodeURIComponent(ip)}/speed-test`)
        queued++
      } catch {
        continue
      }
    }
    setBatchNote(t('nodes.batchQueued', { count: queued }))
    setBatchBusy(false)
    window.setTimeout(() => { void loadNodes() }, 5_000)
  }

  const copySelection = async () => {
    try {
      await navigator.clipboard.writeText(selectedIPs.join('\n'))
      setBatchNote(t('nodes.batchCopied', { count: selectedIPs.length }))
    } catch (reason) {
      setError(errorMessage(reason))
    }
  }

  const exportSelection = () => {
    const rows = [['ip', 'country', 'protocols', 'speedBps', 'pingMs', 'score'], ...nodes.filter(node => selectedSet.has(node.ip)).map(node => [
      node.ip, node.countryShort, node.protocols.join('|'), String(node.speedBps), String(node.pingMs), String(Math.round(node.score)),
    ])]
    const url = URL.createObjectURL(new Blob([rows.map(row => row.join(',')).join('\n')], { type: 'text/csv;charset=utf-8' }))
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = 'proxygate-nodes.csv'
    anchor.click()
    URL.revokeObjectURL(url)
    setBatchNote(t('nodes.batchExported', { count: selectedIPs.length }))
  }

  return <div className="page-stack">
    <header className="page-heading">
      <div><p className="eyebrow">{t('nodes.eyebrow')}</p><h1>{t('nodes.heading')}</h1><p>{t('nodes.description')}</p></div>
      <div className="heading-actions">
        <span className="sync-label">{t('nodes.synced', { time: formatRelativeTime(lastSynced, t) })}</span>
        <button className="secondary-button" disabled={!status?.activeIp || status.state === 'connecting' || reconnecting || switchingDirect} onClick={reconnect}>{reconnecting ? <Spinner /> : <RestartIcon />}{reconnecting ? t('nodes.reconnecting') : t('nodes.reconnectCurrent')}</button>
        <button className="primary-button" disabled={status?.refreshRunning} onClick={refresh}>{status?.refreshRunning ? <Spinner /> : <RefreshIcon />}{status?.refreshRunning ? t('nodes.refreshing') : t('nodes.refreshSource')}</button>
      </div>
    </header>

    {error && <Notice tone="danger">{error}</Notice>}
    {status?.lastError && <Notice tone="warning"><strong>{t('nodes.lastError')}</strong><span>{status.lastError}</span></Notice>}

    <section className="status-grid">
      <article className={`status-card connection ${connected ? 'healthy' : directMode || status?.state === 'connecting' ? 'pending' : 'unhealthy'}`}>
        <div className="status-card-head"><span>{t('status.connection')}</span><i /></div>
        <div className="status-state-row"><strong>{directMode ? t('status.direct') : stateLabel(status?.state, t)}</strong><button className={status?.connectionPaused ? 'resume' : 'direct'} disabled={!status || switchingDirect} onClick={toggleDirect}>{switchingDirect ? <Spinner /> : status?.connectionPaused ? <RestartIcon /> : null}{switchingDirect ? t('nodes.switchingConnection') : status?.connectionPaused ? t('nodes.resumeConnection') : directMode ? t('nodes.pauseReconnect') : t('nodes.enableDirect')}</button></div>
        <p>{directMode ? status?.connectionPaused ? t('status.directPaused') : t('status.directFallback') : status?.selection === 'manual' ? t('status.manual') : t('status.automatic')}</p>
      </article>
      <article className="status-card"><div className="status-card-head"><span>{t('status.currentExit')}</span><small>{status?.activeProtocol ? protocolLabels[status.activeProtocol] : '—'}</small></div><strong className="mono compact">{directMode ? t('status.localNetwork') : status?.activeIp || t('status.notConnected')}</strong><p>{directMode ? t('status.localNetworkHint') : status?.activeHostName || t('status.waiting')}</p></article>
      <article className="status-card"><div className="status-card-head"><span>{t('status.healthCheck')}</span><small>{status?.lastError ? t('status.unhealthy') : t('status.healthy')}</small></div><strong className="compact">{formatRelativeTime(status?.lastHealthCheck, t)}</strong><p>{status?.lastHealthCheck ? formatDateTime(status.lastHealthCheck, locale) : t('status.notChecked')}</p></article>
      <article className="status-card"><div className="status-card-head"><span>{t('status.nodeCount')}</span><small>{t('status.filtered')}</small></div><strong>{nodes.length}</strong><p>{t('status.showing', { count: filteredNodes.length })}</p></article>
    </section>

    <section className="node-panel">
      <div className="node-toolbar">
        <label className="search-box"><SearchIcon /><input type="search" placeholder={t('nodes.search')} value={query} onChange={event => setQuery(event.target.value)} /></label>
        <div className="node-toolbar-right">
          <div className="sort-control"><span>{t('nodes.sort')}</span><select value={sortKey} onChange={event => { sortTouched.current = true; setSortKey(event.target.value as SortKey) }}>{Object.entries(sortLabels).map(([value, label]) => <option value={value} key={value}>{label}</option>)}</select><button title={t('nodes.toggleSort')} onClick={() => { sortTouched.current = true; setSortDirection(direction => direction === 'asc' ? 'desc' : 'asc') }}>{sortDirection === 'asc' ? '↑' : '↓'}</button></div>
          <div className="view-switch" role="group" aria-label={t('nodes.viewMode')}>
            <button type="button" className={viewMode === 'cards' ? 'active' : ''} aria-pressed={viewMode === 'cards'} title={t('nodes.viewCards')} onClick={() => setViewMode('cards')}><GridIcon /><span>{t('nodes.viewCards')}</span></button>
            <button type="button" className={viewMode === 'list' ? 'active' : ''} aria-pressed={viewMode === 'list'} title={t('nodes.viewList')} onClick={() => setViewMode('list')}><ListIcon /><span>{t('nodes.viewList')}</span></button>
            <button type="button" className={viewMode === 'table' ? 'active' : ''} aria-pressed={viewMode === 'table'} title={t('nodes.viewTable')} onClick={() => setViewMode('table')}><TableIcon /><span>{t('nodes.viewTable')}</span></button>
          </div>
        </div>
      </div>
      <div className="node-filter-bar">
        <label><span>{t('nodes.filterCountry')}</span><select value={countryFilter} onChange={event => setCountryFilter(event.target.value)}><option value="">{t('nodes.filterAll')}</option>{countryOptions.map(([code, total]) => <option key={code} value={code}>{countryFlag(code)} {code} ({total})</option>)}</select></label>
        <label><span>{t('nodes.filterProtocol')}</span><select value={protocolFilter} onChange={event => setProtocolFilter(event.target.value as Protocol | '')}><option value="">{t('nodes.filterAll')}</option>{protocolDisplayOrder.map(protocol => <option key={protocol} value={protocol}>{protocolLabels[protocol]}</option>)}</select></label>
        <label><span>{t('nodes.filterMinSpeed')}</span><input type="number" min={0} step={1} value={minSpeedMbps || ''} placeholder="0" onChange={event => setMinSpeedMbps(Math.max(0, Number(event.target.value) || 0))} /></label>
        <label><span>{t('nodes.filterMaxPing')}</span><input type="number" min={0} step={10} value={maxPingMs || ''} placeholder="0" onChange={event => setMaxPingMs(Math.max(0, Number(event.target.value) || 0))} /></label>
        <label><span>{t('nodes.filterMinScore')}</span><input type="number" min={0} step={1000} value={minScore || ''} placeholder="0" onChange={event => setMinScore(Math.max(0, Number(event.target.value) || 0))} /></label>
        {filterActive && <button type="button" className="quiet-button" onClick={resetFilters}>{t('nodes.filterReset')}</button>}
      </div>
      <div className="node-batch-bar">
        <label className="batch-select-all"><input type="checkbox" checked={allFilteredSelected} onChange={event => event.target.checked ? selectAllFiltered() : setSelectedIPs([])} /><span>{t('nodes.batchSelectAll', { count: filteredNodes.length })}</span></label>
        <span className="batch-count">{t('nodes.batchSelected', { count: selectedIPs.length })}</span>
        {selectedIPs.length > 0 && <>
          <button type="button" className="quiet-button" disabled={batchBusy} onClick={() => void batchTest()}>{batchBusy ? <Spinner /> : <SpeedIcon />}{t('nodes.batchTest')}</button>
          <button type="button" className="quiet-button" onClick={() => void copySelection()}>{t('nodes.batchCopy')}</button>
          <button type="button" className="quiet-button" onClick={exportSelection}>{t('nodes.batchExport')}</button>
          <button type="button" className="quiet-button" onClick={() => { setSelectedIPs([]); setBatchNote('') }}>{t('nodes.batchClear')}</button>
        </>}
        {batchNote && <small className="batch-note">{batchNote}</small>}
      </div>
      <div className="node-panel-meta"><span>{t('nodes.count', { count: filteredNodes.length })}</span><span>{t('nodes.perPage', { count: pageSize })}</span></div>
      {loadingNodes ? <NodeSkeleton /> : visibleNodes.length > 0
        ? viewMode === 'cards'
          ? <div className="node-grid">{visibleNodes.map(node => <NodeCard key={node.ip} node={node} protocolPriority={protocolPriority} active={status?.activeIp === node.ip} activeProtocol={status?.activeIp === node.ip ? status?.activeProtocol : undefined} connecting={status?.connectingIp === node.ip} reloadStatus={loadStatus} reloadNodes={loadNodes} selected={selectedSet.has(node.ip)} onToggle={toggleSelected} />)}</div>
          : viewMode === 'list'
            ? <div className="node-list">{visibleNodes.map(node => <NodeRow key={node.ip} node={node} protocolPriority={protocolPriority} active={status?.activeIp === node.ip} activeProtocol={status?.activeIp === node.ip ? status?.activeProtocol : undefined} connecting={status?.connectingIp === node.ip} reloadStatus={loadStatus} selected={selectedSet.has(node.ip)} onToggle={toggleSelected} />)}</div>
            : <NodeTable nodes={visibleNodes} protocolPriority={protocolPriority} status={status} reloadStatus={loadStatus} selected={selectedSet} onToggle={toggleSelected} allSelected={visibleNodes.length > 0 && visibleNodes.every(node => selectedSet.has(node.ip))} onToggleAll={checked => checked ? setSelectedIPs(Array.from(new Set([...selectedIPs, ...visibleNodes.map(node => node.ip)]))) : setSelectedIPs(selectedIPs.filter(ip => !visibleNodes.some(node => node.ip === ip)))} />
        : <EmptyState query={deferredQuery} />}
      {pageCount > 1 && <Pagination page={currentPage} pageCount={pageCount} onChange={setPage} />}
    </section>
  </div>
}

const NodeCard = memo(function NodeCard({ node, protocolPriority, active, activeProtocol, connecting, reloadStatus, reloadNodes, selected, onToggle }: {
  node: Node
  protocolPriority: Protocol[]
  active: boolean
  activeProtocol?: Protocol
  connecting: boolean
  reloadStatus: () => Promise<void>
  reloadNodes: () => Promise<void>
  selected: boolean
  onToggle: (ip: string) => void
}) {
  const { t, i18n } = useTranslation()
  const locale = resolvedLocale(i18n.resolvedLanguage)
  const [testing, setTesting] = useState(false)
  const [startingTest, setStartingTest] = useState(false)
  const [cancelingTest, setCancelingTest] = useState(false)
  const [connectionError, setConnectionError] = useState('')
  const [speedTestError, setSpeedTestError] = useState('')
  const [selecting, setSelecting] = useState(false)
  const [selectedProtocol, setSelectedProtocol] = useState<Protocol | ''>(activeProtocol || '')
  const speedTestRun = useRef(0)
  const availableProtocols = useMemo(() => orderedProtocols(node.protocols, protocolPriority), [node.protocols, protocolPriority])

  useEffect(() => {
    if (active && activeProtocol) setSelectedProtocol(activeProtocol)
  }, [active, activeProtocol])

  const select = async () => {
    setConnectionError(''); setSelecting(true)
    try {
      await api.post('/api/nodes/select', { ip: node.ip, protocol: selectedProtocol })
      await reloadStatus()
    } catch (reason) {
      setConnectionError(errorMessage(reason))
    } finally {
      setSelecting(false)
    }
  }

  const test = async () => {
    const run = ++speedTestRun.current
    setSpeedTestError(''); setTesting(true); setStartingTest(true)
    try {
      await api.post(`/api/nodes/${encodeURIComponent(node.ip)}/speed-test`)
      if (speedTestRun.current !== run) return
      setStartingTest(false)
      while (speedTestRun.current === run) {
        await delay(1_500)
        if (speedTestRun.current !== run) return
        const result = await api.get<{ state: string; error?: string }>(`/api/nodes/${encodeURIComponent(node.ip)}/speed-test`)
        if (result.state === 'failed') throw new Error(result.error || t('node.speedTestFailed'))
        if (result.state === 'canceled') return
        if (result.state === 'complete') { await reloadNodes(); return }
      }
    } catch (reason) {
      if (speedTestRun.current === run) setSpeedTestError(errorMessage(reason))
    } finally {
      if (speedTestRun.current === run) { setTesting(false); setStartingTest(false) }
    }
  }

  const cancelTest = async () => {
    const run = speedTestRun.current
    setCancelingTest(true)
    try {
      const result = await api.delete<{ state: string; error?: string }>(`/api/nodes/${encodeURIComponent(node.ip)}/speed-test`)
      if (speedTestRun.current !== run) return
      speedTestRun.current++
      if (result.state === 'complete') await reloadNodes()
      setSpeedTestError(result.state === 'failed' ? result.error || t('node.speedTestFailed') : '')
      setTesting(false)
      setStartingTest(false)
    } catch (reason) {
      if (speedTestRun.current === run) setSpeedTestError(errorMessage(reason))
    } finally {
      setCancelingTest(false)
    }
  }

  return <article className={`node-card ${active ? 'active' : ''} ${selected ? 'selected' : ''}`}>
    <div className="node-identity">
      <label className="node-check"><input type="checkbox" checked={selected} onChange={() => onToggle(node.ip)} aria-label={`${t('nodes.batchSelected', { count: 1 })} ${node.ip}`} /></label>
      <div className="country-mark" aria-label={node.countryLong}>{countryFlag(node.countryShort)}</div>
      <div><div className="node-title"><strong>{node.countryShort || '—'}</strong>{active && <span className="live-badge">{t('node.active')}</span>}{connecting && <span className="pending-badge">{t('node.connecting')}</span>}</div><span className="mono">{node.ip}</span></div>
    </div>
    <p className="node-operator" title={node.message}>{node.operator || node.hostName || t('node.unknownOperator')}</p>
    <div className="protocol-list">{availableProtocols.map(protocol => <span key={protocol}>{protocolLabels[protocol]}</span>)}</div>
    <dl className="node-metrics">
      <div><dt>{t('node.vpnGate')}</dt><dd>{formatRate(node.speedBps)}</dd></div>
      <div><dt>{t('node.measured')}</dt><dd className={speedTestError || node.speedTestFailed ? 'failed' : node.measuredBps ? 'accent' : ''}>{speedTestError ? <ErrorTooltip label={t('node.failed')} message={speedTestError} /> : node.speedTestFailed ? t('node.failed') : formatRate(node.measuredBps)}</dd></div>
      <div><dt>{t('node.latency')}</dt><dd>{node.pingMs > 0 ? `${node.pingMs} ms` : '—'}</dd></div>
      <div><dt>{t('node.score')}</dt><dd>{formatNumber(node.score, locale)}</dd></div>
    </dl>
    <label className="connection-protocol"><span>{t('node.protocol')}</span><select value={selectedProtocol} disabled={connecting || selecting} onChange={event => { setSelectedProtocol(event.target.value as Protocol | ''); setConnectionError('') }}><option value="">{t('node.priority')}</option>{availableProtocols.map(protocol => <option value={protocol} key={protocol}>{protocolLabels[protocol]}</option>)}</select></label>
    <div className="node-actions"><button className={`secondary-button ${connectionError ? 'error-tooltip' : ''}`} data-tooltip={connectionError || undefined} aria-label={connectionError ? t('node.connectFailedLabel', { error: connectionError }) : undefined} disabled={connecting || selecting} onClick={select}>{connecting || selecting ? <><Spinner />{t('node.connectingAction')}</> : connectionError ? t('node.connectFailed') : active ? selectedProtocol === '' ? t('node.reconnectPriority') : selectedProtocol === activeProtocol ? t('node.reconnect') : t('node.reconnectProtocol') : t('node.switch')}</button><button className={`icon-button ${testing ? 'cancel-button' : ''}`} title={testing ? t('node.cancelSpeedTestTitle') : t('node.speedTestTitle')} disabled={startingTest || cancelingTest} onClick={testing ? cancelTest : test}>{startingTest || cancelingTest ? <Spinner /> : testing ? <StopIcon /> : <SpeedIcon />}<span>{cancelingTest ? t('node.cancelingTest') : startingTest ? t('node.testing') : testing ? t('node.cancelTest') : t('node.speedTest')}</span></button><NodeShareButton node={node} /></div>
  </article>
})

function ErrorTooltip({ label, message }: { label: string; message: string }) {
  return <span className="error-tooltip" tabIndex={0} data-tooltip={message} aria-label={`${label}: ${message}`}>{label}</span>
}

function NodeStateBadge({ active, connecting, activeProtocol }: { active: boolean; connecting: boolean; activeProtocol?: Protocol }) {
  const { t } = useTranslation()
  if (active) return <span className="live-badge">{t('node.active')}{activeProtocol ? ` · ${protocolLabels[activeProtocol]}` : ''}</span>
  if (connecting) return <span className="pending-badge">{t('node.connecting')}</span>
  return <span className="idle-badge">—</span>
}

function NodeSelectButton({ node, active, connecting, reloadStatus }: { node: Node; active: boolean; connecting: boolean; reloadStatus: () => Promise<void> }) {
  const { t } = useTranslation()
  const [selecting, setSelecting] = useState(false)
  const [error, setError] = useState('')

  const select = async () => {
    setError(''); setSelecting(true)
    try {
      await api.post('/api/nodes/select', { ip: node.ip, protocol: '' })
      await reloadStatus()
    } catch (reason) {
      setError(errorMessage(reason))
    } finally {
      setSelecting(false)
    }
  }

  return <button type="button" className={`secondary-button compact-action ${error ? 'error-tooltip' : ''}`} data-tooltip={error || undefined} aria-label={error ? t('node.connectFailedLabel', { error }) : undefined} disabled={connecting || selecting} onClick={select}>{connecting || selecting ? <><Spinner />{t('node.connectingAction')}</> : error ? t('node.connectFailed') : active ? t('node.reconnectPriority') : t('node.switch')}</button>
}

function nodeShareLink(node: Node) {
  if (!node.port) return ''
  if (node.protocols.includes('socks5')) return `socks5://${node.ip}:${node.port}#${encodeURIComponent(node.countryShort || node.ip)}`
  if (node.protocols.includes('http')) return `http://${node.ip}:${node.port}#${encodeURIComponent(node.countryShort || node.ip)}`
  return ''
}

function NodeShareButton({ node, compact = false }: { node: Node; compact?: boolean }) {
  const { t } = useTranslation()
  const [copied, setCopied] = useState(false)
  const link = nodeShareLink(node)
  if (!link) return null
  const copy = async () => {
    try {
      await writeClipboard(link)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1_600)
    } catch { /* clipboard unavailable */ }
  }
  return <button type="button" className={`icon-button ${compact ? 'compact-action' : ''}`} title={copied ? t('nodes.copiedLink') : link} onClick={copy}><LinkIcon /><span>{copied ? t('nodes.copiedLink') : t('nodes.copyLink')}</span></button>
}

const NodeRow = memo(function NodeRow({ node, protocolPriority, active, activeProtocol, connecting, reloadStatus, selected, onToggle }: {
  node: Node
  protocolPriority: Protocol[]
  active: boolean
  activeProtocol?: Protocol
  connecting: boolean
  reloadStatus: () => Promise<void>
  selected: boolean
  onToggle: (ip: string) => void
}) {
  const { t } = useTranslation()
  const availableProtocols = orderedProtocols(node.protocols, protocolPriority)
  return <article className={`node-row ${active ? 'active' : ''} ${selected ? 'selected' : ''}`}>
    <div className="node-row-identity">
      <label className="node-check"><input type="checkbox" checked={selected} onChange={() => onToggle(node.ip)} aria-label={node.ip} /></label>
      <span className="country-mark small" aria-label={node.countryLong}>{countryFlag(node.countryShort)}</span>
      <div><strong>{node.countryShort || '—'} <span className="mono">{node.ip}</span></strong><small title={node.message}>{node.operator || node.hostName || t('node.unknownOperator')}</small></div>
    </div>
    <div className="protocol-list compact">{availableProtocols.map(protocol => <span key={protocol}>{protocolLabels[protocol]}</span>)}</div>
    <div className="node-row-metrics">
      <span><small>{t('node.vpnGate')}</small><b>{formatRate(node.speedBps)}</b></span>
      <span><small>{t('node.measured')}</small><b className={node.speedTestFailed ? 'failed' : node.measuredBps ? 'accent' : ''}>{node.speedTestFailed ? t('node.failed') : formatRate(node.measuredBps)}</b></span>
      <span><small>{t('node.latency')}</small><b>{node.pingMs > 0 ? `${node.pingMs} ms` : '—'}</b></span>
      <span><small>{t('node.score')}</small><b>{Math.round(node.score)}</b></span>
    </div>
    <div className="node-row-state"><NodeStateBadge active={active} connecting={connecting} activeProtocol={activeProtocol} /></div>
    <div className="node-row-actions"><NodeSelectButton node={node} active={active} connecting={connecting} reloadStatus={reloadStatus} /><NodeShareButton node={node} compact /></div>
  </article>
})

function NodeTable({ nodes, protocolPriority, status, reloadStatus, selected, onToggle, allSelected, onToggleAll }: {
  nodes: Node[]
  protocolPriority: Protocol[]
  status?: Status
  reloadStatus: () => Promise<void>
  selected: Set<string>
  onToggle: (ip: string) => void
  allSelected: boolean
  onToggleAll: (checked: boolean) => void
}) {
  const { t } = useTranslation()
  return <div className="node-table-wrap">
    <table className="node-table">
      <thead><tr>
        <th className="select-col"><input type="checkbox" checked={allSelected} onChange={event => onToggleAll(event.target.checked)} aria-label={t('nodes.batchSelectAll', { count: nodes.length })} /></th>
        <th>{t('nodes.tableNode')}</th>
        <th>{t('nodes.tableOperator')}</th>
        <th>{t('nodes.tableProtocols')}</th>
        <th className="num">{t('node.vpnGate')}</th>
        <th className="num">{t('node.measured')}</th>
        <th className="num">{t('node.latency')}</th>
        <th className="num">{t('node.score')}</th>
        <th>{t('nodes.tableState')}</th>
        <th />
      </tr></thead>
      <tbody>
        {nodes.map(node => {
          const active = status?.activeIp === node.ip
          const connecting = status?.connectingIp === node.ip
          return <tr key={node.ip} className={`${active ? 'active' : ''} ${selected.has(node.ip) ? 'selected' : ''}`}>
            <td className="select-col"><input type="checkbox" checked={selected.has(node.ip)} onChange={() => onToggle(node.ip)} aria-label={node.ip} /></td>
            <td><div className="table-node"><span className="country-mark small">{countryFlag(node.countryShort)}</span><div><strong>{node.countryShort || '—'}</strong><span className="mono">{node.ip}</span></div></div></td>
            <td><span className="table-operator" title={node.message}>{node.operator || node.hostName || t('node.unknownOperator')}</span></td>
            <td><div className="protocol-list compact">{orderedProtocols(node.protocols, protocolPriority).map(protocol => <span key={protocol}>{protocolLabels[protocol]}</span>)}</div></td>
            <td className="num">{formatRate(node.speedBps)}</td>
            <td className={`num ${node.speedTestFailed ? 'failed' : node.measuredBps ? 'accent' : ''}`}>{node.speedTestFailed ? t('node.failed') : formatRate(node.measuredBps)}</td>
            <td className="num">{node.pingMs > 0 ? `${node.pingMs} ms` : '—'}</td>
            <td className="num">{Math.round(node.score)}</td>
            <td><NodeStateBadge active={active} connecting={connecting} activeProtocol={active ? status?.activeProtocol : undefined} /></td>
            <td><div className="node-row-actions"><NodeSelectButton node={node} active={active} connecting={connecting} reloadStatus={reloadStatus} /><NodeShareButton node={node} compact /></div></td>
          </tr>
        })}
      </tbody>
    </table>
  </div>
}

function GridIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="3" width="7" height="7" rx="1.5" /><rect x="14" y="3" width="7" height="7" rx="1.5" /><rect x="3" y="14" width="7" height="7" rx="1.5" /><rect x="14" y="14" width="7" height="7" rx="1.5" /></svg> }
function ListIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 6h16M4 12h16M4 18h16" /></svg> }
function TableIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="4" width="18" height="16" rx="2" /><path d="M3 9h18M9 9v11M15 9v11" /></svg> }

function Pagination({ page, pageCount, onChange }: { page: number; pageCount: number; onChange: (page: number) => void }) {
  const { t } = useTranslation()
  const pages = Array.from({ length: Math.min(5, pageCount) }, (_, index) => {
    const start = Math.max(1, Math.min(page - 2, pageCount - 4))
    return start + index
  })
  return <nav className="pagination" aria-label={t('pagination.label')}><button disabled={page === 1} onClick={() => onChange(page - 1)}>{t('pagination.previous')}</button>{pages.map(value => <button className={value === page ? 'active' : ''} key={value} onClick={() => onChange(value)}>{value}</button>)}<button disabled={page === pageCount} onClick={() => onChange(page + 1)}>{t('pagination.next')}</button></nav>
}

function NodeSkeleton() {
  return <div className="node-grid">{Array.from({ length: 6 }, (_, index) => <div className="node-card skeleton-card" key={index}><i /><i /><i /><i /></div>)}</div>
}

function EmptyState({ query }: { query: string }) {
  const { t } = useTranslation()
  return <div className="empty-state"><SearchIcon /><h3>{query ? t('nodes.noMatches') : t('nodes.noNodes')}</h3><p>{query ? t('nodes.shortenSearch') : t('nodes.refreshFirst')}</p></div>
}

type SubscriptionTab = 'links' | 'clients' | 'builder' | 'pool' | 'front'

function Subscription() {
  const { t } = useTranslation()
  const [info, setInfo] = useState<SubscriptionInfo>()
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [copied, setCopied] = useState('')
  const [tab, setTab] = useState<SubscriptionTab>('links')

  const load = useCallback(() => {
    setLoading(true)
    api.get<SubscriptionInfo>('/api/subscription')
      .then(value => { setInfo(value); setError('') })
      .catch(reason => setError(errorMessage(reason)))
      .finally(() => setLoading(false))
  }, [])

  useEffect(load, [load])

  const copy = async (key: string, value: string) => {
    if (!value) return
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(value)
      } else {
        const field = document.createElement('textarea')
        field.value = value
        field.style.position = 'fixed'
        field.style.opacity = '0'
        document.body.appendChild(field)
        field.select()
        document.execCommand('copy')
        field.remove()
      }
      setCopied(key)
      window.setTimeout(() => setCopied(current => current === key ? '' : current), 1600)
    } catch { /* clipboard unavailable */ }
  }

  return <div className="page-stack settings-page">
    <header className="page-heading">
      <div><p className="eyebrow">{t('subscription.eyebrow')}</p><h1>{t('subscription.heading')}</h1><p>{t('subscription.description')}</p></div>
      <button className="quiet-button" disabled={loading} onClick={load}>{loading ? <Spinner /> : <RefreshIcon />}{t('subscription.refresh')}</button>
    </header>
    <nav className="sub-tabs" role="tablist" aria-label={t('subscription.tabs.label')}>
      {(['links', 'clients', 'builder', 'pool', 'front'] as SubscriptionTab[]).map(value => <button key={value} role="tab" aria-selected={tab === value} className={tab === value ? 'active' : ''} onClick={() => setTab(value)}>{t(`subscription.tabs.${value}`)}</button>)}
    </nav>
    {error && <Notice tone="danger">{error}</Notice>}
    {info && !info.enabled && <Notice tone="warning">{t('subscription.disabled')}</Notice>}
    {info && info.enabled && tab === 'links' && <SubscriptionLinks info={info} copy={copy} copied={copied} />}
    {info && tab === 'clients' && <SubscriptionClients info={info} copy={copy} copied={copied} onChanged={load} />}
    {info && tab === 'builder' && <SubscriptionBuilder info={info} copy={copy} copied={copied} onOpenPool={() => setTab('pool')} />}
    {info && tab === 'pool' && <MatrixPool onChanged={load} />}
    {info && tab === 'front' && <FrontProxyPanel initial={info.frontProxy} onSaved={load} />}
  </div>
}

type ShareFormat = 'socks5' | 'socks5h' | 'v2ray' | 'hostport' | 'json' | 'csv'

const shareFormats: ShareFormat[] = ['socks5', 'socks5h', 'v2ray', 'hostport', 'json', 'csv']

function shareSlotLine(slot: ShareSlot, format: ShareFormat) {
  switch (format) {
    case 'socks5': return slot.socks5
    case 'socks5h': return slot.socks5h
    case 'v2ray': return slot.v2ray
    case 'hostport': return `${slot.host}:${slot.port}:${slot.username}:${slot.password}`
    case 'json': return JSON.stringify({ host: slot.host, port: slot.port, username: slot.username, password: slot.password, country: slot.country || slot.countryShort || '', slot: slot.slotId })
    case 'csv': return [slot.host, slot.port, slot.username, slot.password, slot.country || slot.countryShort || '', slot.slotId].join(',')
  }
}

function shareFileName(format: ShareFormat) {
  const names: Record<ShareFormat, string> = {
    socks5: 'proxygate-socks5.txt',
    socks5h: 'proxygate-socks5h.txt',
    v2ray: 'proxygate-v2ray.txt',
    hostport: 'proxygate-hostport.txt',
    json: 'proxygate-proxies.jsonl',
    csv: 'proxygate-proxies.csv',
  }
  return names[format]
}

type ShareTab = 'extract' | 'subscription' | 'local' | 'guide'

const shareTabs: Array<{ id: ShareTab; index: string; label: string }> = [
  { id: 'extract', index: '01', label: 'share.tabExtract' },
  { id: 'subscription', index: '02', label: 'share.tabSubscription' },
  { id: 'local', index: '03', label: 'share.tabLocal' },
  { id: 'guide', index: '04', label: 'share.tabGuide' },
]

function Share() {
  const { t } = useTranslation()
  const [pool, setPool] = useState<SharePoolInfo>()
  const [info, setInfo] = useState<SubscriptionInfo>()
  const [error, setError] = useState('')
  const [note, setNote] = useState('')
  const [loading, setLoading] = useState(true)
  const [copied, setCopied] = useState('')
  const [shareTab, setShareTab] = useState<ShareTab>('extract')
  const [format, setFormat] = useState<ShareFormat>('socks5')
  const [country, setCountry] = useState('')
  const [state, setState] = useState('')
  const [query, setQuery] = useState('')
  const [selected, setSelected] = useState<number[]>([])
  const [page, setPage] = useState(1)
  const [qrValue, setQrValue] = useState('')
  const [localPort, setLocalPort] = useState(() => {
    const stored = Number(window.localStorage.getItem('proxygate:local-port'))
    return Number.isFinite(stored) && stored >= 1024 ? stored : 12001
  })
  const [local, setLocal] = useState<LocalPlan>()

  const load = useCallback(async (silent = false) => {
    if (!silent) setLoading(true)
    try {
      const [nextPool, nextInfo] = await Promise.all([
        api.get<SharePoolInfo>('/api/share/pool'),
        api.get<SubscriptionInfo>('/api/subscription').catch(() => undefined),
      ])
      setPool(nextPool)
      if (nextInfo) setInfo(nextInfo)
      setError('')
    } catch (reason) {
      setError(errorMessage(reason))
    } finally {
      if (!silent) setLoading(false)
    }
  }, [])

  const loadLocal = useCallback(async (port: number) => {
    try {
      setLocal(await api.get<LocalPlan>(`/api/local/plan?basePort=${port}`))
    } catch {
      // keep the previous plan while the panel is briefly unavailable
    }
  }, [])

  useEffect(() => { void load() }, [load])
  useEffect(() => {
    window.localStorage.setItem('proxygate:local-port', String(localPort))
    void loadLocal(localPort)
  }, [localPort, loadLocal])
  useEffect(() => {
    const timer = window.setInterval(() => { void load(true); void loadLocal(localPort) }, 8_000)
    return () => window.clearInterval(timer)
  }, [load, loadLocal, localPort])

  const copy = async (key: string, value: string) => {
    if (!value) return
    try {
      await writeClipboard(value)
      setCopied(key)
      window.setTimeout(() => setCopied(current => current === key ? '' : current), 1_600)
    } catch (reason) {
      setError(errorMessage(reason))
    }
  }

  const countries = useMemo(() => {
    const counts = new Map<string, number>()
    for (const slot of pool?.slots ?? []) {
      const code = (slot.country || slot.countryShort || '').toUpperCase()
      if (code) counts.set(code, (counts.get(code) ?? 0) + 1)
    }
    return Array.from(counts.entries()).sort((left, right) => right[1] - left[1] || left[0].localeCompare(right[0]))
  }, [pool])

  const slots = useMemo(() => {
    const needle = query.trim().toLowerCase()
    return (pool?.slots ?? []).filter(slot => {
      const code = (slot.country || slot.countryShort || '').toUpperCase()
      if (country && code !== country) return false
      if (state === 'healthy' && !slot.healthy) return false
      if (state === 'ready' && slot.state !== 'ready') return false
      if (state === 'switching' && slot.state !== 'switching') return false
      if (state === 'error' && slot.state !== 'error') return false
      if (!needle) return true
      return [String(slot.slotId), slot.host, String(slot.port), slot.username, slot.exitIp ?? '', code, slot.asnOrg ?? '', slot.source ?? ''].some(value => value.toLowerCase().includes(needle))
    })
  }, [pool, country, state, query])

  useEffect(() => { setPage(1) }, [country, state, query])

  const pageSize = 12
  const pageCount = Math.max(1, Math.ceil(slots.length / pageSize))
  const currentPage = Math.min(page, pageCount)
  const pageSlots = slots.slice((currentPage - 1) * pageSize, currentPage * pageSize)
  const selectedSet = useMemo(() => new Set(selected), [selected])
  const selectedSlots = useMemo(() => (pool?.slots ?? []).filter(slot => selectedSet.has(slot.slotId)), [pool, selectedSet])
  const allSelected = slots.length > 0 && slots.every(slot => selectedSet.has(slot.slotId))

  const toggleSelected = (id: number) => setSelected(current => current.includes(id) ? current.filter(value => value !== id) : [...current, id])
  const toggleAll = (checked: boolean) => setSelected(current => checked
    ? Array.from(new Set([...current, ...slots.map(slot => slot.slotId)]))
    : current.filter(id => !slots.some(slot => slot.slotId === id)))

  const copyMany = async (key: string, list: ShareSlot[]) => {
    if (list.length === 0) return
    await copy(key, list.map(slot => shareSlotLine(slot, format)).join('\n'))
    setNote(t('share.copiedMany', { count: list.length }))
  }

  const exportMany = (list: ShareSlot[]) => {
    if (list.length === 0) return
    const lines = list.map(slot => shareSlotLine(slot, format))
    const header = format === 'csv' ? 'host,port,username,password,country,slot\n' : ''
    const blob = new Blob([header + lines.join('\n')], { type: format === 'csv' ? 'text/csv;charset=utf-8' : 'text/plain;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = shareFileName(format)
    anchor.click()
    URL.revokeObjectURL(url)
    setNote(t('share.exportedMany', { count: list.length }))
  }

  const resetFilters = () => { setCountry(''); setState(''); setQuery('') }

  const subscriptionBase = info?.autoUrl || (info?.v2rayUrl ? info.v2rayUrl.replace(/\/v2ray$/, '') : '')
  const subscriptionLinks = [
    { key: 'v2ray', label: t('subscription.v2rayLabel'), hint: t('share.subV2ray'), value: info?.v2rayUrl ?? '' },
    { key: 'clash', label: t('subscription.clashLabel'), hint: t('share.subClash'), value: info?.clashUrl ?? '' },
    { key: 'singbox', label: t('share.subSingbox'), hint: t('share.subSingboxHint'), value: info?.singboxUrl ?? '' },
    { key: 'auto', label: t('subscription.autoLabel'), hint: t('share.subAuto'), value: info?.autoUrl ?? '' },
    { key: 'socksb64', label: t('share.subSocksB64'), hint: t('share.subSocksB64Hint'), value: subscriptionBase ? `${subscriptionBase}/socksb64` : '' },
    { key: 'socks5', label: t('share.subSocksPlain'), hint: t('share.subSocksPlainHint'), value: subscriptionBase ? `${subscriptionBase}/socks5` : '' },
  ].filter(link => link.value)

  return <div className="page-stack settings-page">
    <header className="page-heading">
      <div><p className="eyebrow">{t('share.eyebrow')}</p><h1>{t('share.heading')}</h1><p>{t('share.description')}</p></div>
      <button className="quiet-button" disabled={loading} onClick={() => void load()}>{loading ? <Spinner /> : <RefreshIcon />}{t('share.refresh')}</button>
    </header>
    {error && <Notice tone="danger">{error}</Notice>}
    {pool && pool.direct && (pool.host === '127.0.0.1' || pool.host === 'localhost') && <Notice tone="warning">{t('share.hostWarning')}</Notice>}

    <section className="share-hero">
      <div className="share-hero-copy">
        <p className="eyebrow">{t('share.heroEyebrow')}</p>
        <h2>{t('share.heroTitle')}</h2>
        <p>{t('share.heroHint')}</p>
      </div>
      <div className="share-stats">
        <div><span>{t('share.statSlots')}</span><strong>{pool?.total ?? 0}</strong><small>{t('share.statSlotsHint', { max: pool?.maxSlots ?? 0 })}</small></div>
        <div><span>{t('share.statHealthy')}</span><strong>{pool?.healthy ?? 0}</strong><small>{t('share.statHealthyHint', { ready: pool?.ready ?? 0 })}</small></div>
        <div><span>{t('share.statHost')}</span><strong className="share-host mono">{pool?.host || '—'}</strong><small>{t('share.statHostHint')}</small></div>
      </div>
    </section>

    <nav className="settings-tabs share-tabs" role="tablist" aria-label={t('share.heading')}>
      {shareTabs.map(tab => <button key={tab.id} type="button" role="tab" id={`share-tab-${tab.id}`} aria-controls={`share-panel-${tab.id}`} aria-selected={shareTab === tab.id} className={shareTab === tab.id ? 'active' : ''} onClick={() => setShareTab(tab.id)}><b>{tab.index}</b><span>{t(tab.label)}</span>{tab.id === 'extract' && <small>{pool?.total ?? 0}</small>}{tab.id === 'local' && <small>{local?.total ?? 0}</small>}</button>)}
    </nav>

    <div className="settings-tab-panel" role="tabpanel" id={`share-panel-${shareTab}`} aria-labelledby={`share-tab-${shareTab}`}>
    {shareTab === 'extract' && <SettingsSection stacked index="01" title={t('share.extractTitle')} description={t('share.extractHint')}>
      {pool && !pool.direct && <Notice tone="warning">{t('share.hostWarningInternal')}</Notice>}
      <div className="share-filter wide">
        <label><span>{t('share.filterCountry')}</span><select value={country} onChange={event => setCountry(event.target.value)}><option value="">{t('share.filterAll')}</option>{countries.map(([code, total]) => <option key={code} value={code}>{countryFlag(code)} {code} ({total})</option>)}</select></label>
        <label><span>{t('share.filterState')}</span><select value={state} onChange={event => setState(event.target.value)}><option value="">{t('share.filterAll')}</option><option value="ready">{t('share.stateReady')}</option><option value="healthy">{t('share.stateHealthy')}</option><option value="switching">{t('share.stateSwitching')}</option><option value="error">{t('share.stateError')}</option></select></label>
        <label className="share-search"><span>{t('share.search')}</span><input type="search" placeholder={t('share.searchPlaceholder')} value={query} onChange={event => setQuery(event.target.value)} /></label>
        {(country || state || query) && <button type="button" className="quiet-button" onClick={resetFilters}>{t('share.filterReset')}</button>}
      </div>
      <div className="share-format wide">
        <div className="share-format-head"><strong>{t('share.formatLabel')}</strong><small>{t('share.formatHint')}</small></div>
        <div className="share-format-grid">{shareFormats.map(value => <button type="button" key={value} aria-pressed={format === value} className={format === value ? 'active' : ''} onClick={() => setFormat(value)}>{t(`share.formats.${value}`)}</button>)}</div>
      </div>
      <div className="share-bulk wide">
        <label className="batch-select-all"><input type="checkbox" checked={allSelected} onChange={event => toggleAll(event.target.checked)} /><span>{t('share.selectAll', { count: slots.length })}</span></label>
        <span className="batch-count">{t('share.selected', { count: selected.length })}</span>
        <button type="button" className="quiet-button" disabled={selected.length === 0} onClick={() => void copyMany('selected', selectedSlots)}>{t('share.copySelected')}</button>
        <button type="button" className="quiet-button" disabled={slots.length === 0} onClick={() => void copyMany('all', slots)}>{t('share.copyAll')}</button>
        <button type="button" className="quiet-button" disabled={selected.length === 0} onClick={() => exportMany(selectedSlots)}>{t('share.exportSelected')}</button>
        <button type="button" className="quiet-button" disabled={slots.length === 0} onClick={() => exportMany(slots)}>{t('share.exportAll')}</button>
        {note && <small className="batch-note">{note}</small>}
      </div>
      {loading && !pool ? <div className="panel-loading wide"><Spinner />{t('share.loading')}</div> : slots.length === 0 ? <p className="muted-line wide">{t('share.empty')}</p> : <div className="node-table-wrap wide">
        <table className="node-table share-table">
          <thead><tr>
            <th className="select-col"><input type="checkbox" checked={allSelected} onChange={event => toggleAll(event.target.checked)} aria-label={t('share.selectAll', { count: slots.length })} /></th>
            <th>{t('share.tableSlot')}</th>
            <th>{t('share.tableCountry')}</th>
            <th>{t('share.tableExit')}</th>
            <th>{t('share.tableEndpoint')}</th>
            <th>{t('share.tableState')}</th>
            <th>{t('share.tableQuality')}</th>
            <th>{t('share.tableSource')}</th>
            <th>{t('share.tableActions')}</th>
          </tr></thead>
          <tbody>
            {pageSlots.map(slot => {
              const code = slot.country || slot.countryShort || ''
              const copyKey = `slot-${slot.slotId}`
              return <tr key={slot.slotId} className={selectedSet.has(slot.slotId) ? 'selected' : ''}>
                <td className="select-col"><input type="checkbox" checked={selectedSet.has(slot.slotId)} onChange={() => toggleSelected(slot.slotId)} aria-label={`#${slot.slotId}`} /></td>
                <td className="mono">#{String(slot.slotId).padStart(2, '0')}<br /><small>:{slot.port}</small></td>
                <td>{code ? <><span className="country-mark small">{countryFlag(code)}</span> <span className="mono">{code}</span>{slot.asnOrg ? <small title={slot.asnOrg}> · {slot.asnOrg}</small> : null}</> : '—'}</td>
                <td className="mono">{slot.exitIp || slot.activeIp || '—'}</td>
                <td className="mono">{slot.host}:{slot.port}<br /><small>{slot.username}</small></td>
                <td>{slot.healthy ? <span className="live-badge">{t('share.stateHealthy')}</span> : <span className="pending-badge">{slot.state === 'ready' ? t('share.unhealthy') : slot.state}</span>}</td>
                <td className="mono">{slot.qualityScore ? Math.round(slot.qualityScore) : '—'}<br /><small>{formatRate(slot.speedBps)} · {slot.pingMs > 0 ? `${slot.pingMs} ms` : '—'}</small></td>
                <td>{slot.source ? <span className="mini-badge" title={slot.source}>{slot.source}</span> : '—'}</td>
                <td><div className="share-row-actions">
                  <button type="button" className="quiet-button" onClick={() => void copy(copyKey, shareSlotLine(slot, format))}>{copied === copyKey ? t('share.copied') : t('share.copy')}</button>
                  <button type="button" className="quiet-button" onClick={() => setQrValue(shareSlotLine(slot, format))}>{t('share.qr')}</button>
                </div></td>
              </tr>
            })}
          </tbody>
        </table>
      </div>}
      {pageCount > 1 && <div className="wide"><Pagination page={currentPage} pageCount={pageCount} onChange={setPage} /></div>}
    </SettingsSection>}

    {shareTab === 'subscription' && <SettingsSection stacked index="02" title={t('share.subscriptionTitle')} description={t('share.subscriptionHint')}>
      <div className="share-sub-grid wide">
        {subscriptionLinks.map(link => <article className="share-sub-card" key={link.key}>
          <div><strong>{link.label}</strong><small>{link.hint}</small></div>
          <code title={link.value}>{link.value}</code>
          <div className="share-sub-actions">
            <button type="button" className="quiet-button" onClick={() => void copy(`sub-${link.key}`, link.value)}>{copied === `sub-${link.key}` ? t('share.copied') : t('share.copy')}</button>
            <button type="button" className="quiet-button" onClick={() => setQrValue(link.value)}>{t('share.qr')}</button>
          </div>
        </article>)}
      </div>
    </SettingsSection>}

    {shareTab === 'guide' && <SettingsSection stacked index="04" title={t('share.guideTitle')} description={t('share.guideDescription')}>
      <div className="share-guide-grid wide">
        <article><strong>{t('share.guideV2rayTitle')}</strong><p>{t('share.guideV2ray')}</p></article>
        <article><strong>{t('share.guideBrowserTitle')}</strong><p>{t('share.guideBrowser')}</p></article>
      </div>
      <p className="section-note wide">{t('share.guideNote')}</p>
    </SettingsSection>}

    {shareTab === 'local' && <SettingsSection stacked index="03" title={t('share.localTitle')} description={t('share.localDescription')}>
      <div className="share-filter wide">
        <label><span>{t('share.localBasePort')}</span><input type="number" min={1024} max={65000} value={localPort} onChange={event => setLocalPort(Math.max(1024, Math.min(65000, Number(event.target.value) || 12001)))} /></label>
      </div>
      <div className="share-bulk wide">
        <button type="button" className="quiet-button" disabled={!local?.total} onClick={() => void copy('local-list', (local?.nodes ?? []).map(node => node.socks5).join('\n'))}>{copied === 'local-list' ? t('share.copied') : t('share.localCopy')}</button>
        <a className="quiet-button" href={`/api/local/singbox?basePort=${localPort}`} download="proxygate-singbox.json">{t('share.localDownloadConfig')}</a>
        <a className="quiet-button" href={`/api/local/bundle?basePort=${localPort}`} download="proxygate-local.zip">{t('share.localDownloadBundle')}</a>
        <a className="quiet-button" href="https://ghfast.top/https://github.com/SagerNet/sing-box/releases/download/v1.14.4/sing-box-1.14.4-windows-amd64.zip" target="_blank" rel="noreferrer">{t('share.localDownloadSingbox')}</a>
        <span className="batch-count">{t('share.localCount', { count: local?.total ?? 0 })}</span>
      </div>
      {local?.missingHost && <Notice tone="warning">{t('share.localMissingHost')}</Notice>}
      <p className="section-note wide">{t('share.localHint', { host: local?.host || '—' })}</p>
      <pre className="local-list wide">{local?.nodes.length ? local.nodes.map(node => `${node.socks5}  ${node.name}`).join('\n') : t('share.localEmpty')}</pre>
    </SettingsSection>}
    </div>

    {qrValue && <div className="share-qr-overlay" role="presentation" onClick={() => setQrValue('')}>
      <div className="share-qr-dialog" role="dialog" aria-modal="true" onClick={event => event.stopPropagation()}>
        <QRCodeSVG value={qrValue} size={200} level="M" marginSize={2} bgColor="#ffffff" fgColor="#111827" />
        <code>{qrValue}</code>
        <div className="share-qr-actions">
          <button type="button" className="primary-button" onClick={() => void copy('qr', qrValue)}>{copied === 'qr' ? t('share.copied') : t('share.copy')}</button>
          <button type="button" className="quiet-button" onClick={() => setQrValue('')}>{t('share.close')}</button>
        </div>
      </div>
    </div>}
  </div>
}

function LinkField({ fieldKey, label, hint, value, copied, copy }: { fieldKey: string; label: string; hint: string; value: string; copied: string; copy: (key: string, value: string) => void }) {
  const { t } = useTranslation()
  return <Field label={label} hint={hint} wide>
    <div className="link-copy">
      <input readOnly value={value} placeholder="—" onFocus={event => event.currentTarget.select()} />
      <button type="button" className="quiet-button" disabled={!value} onClick={() => copy(fieldKey, value)}>{copied === fieldKey ? t('subscription.copied') : t('subscription.copy')}</button>
    </div>
  </Field>
}

function SubscriptionLinks({ info, copy, copied }: { info: SubscriptionInfo; copy: (key: string, value: string) => void; copied: string }) {
  const { t } = useTranslation()
  const subscriptionBase = info.autoUrl || (info.v2rayUrl ? info.v2rayUrl.replace(/\/v2ray$/, '') : '')
  const [showAllCountries, setShowAllCountries] = useState(false)
  const presetLimit = 20
  const presetOptions = (preset: string) => ({ preset, count: String(presetLimit) })
  const presets = [
    { key: 'all', label: t('subscription.presetAllLabel'), hint: t('subscription.presetAllHint'), clash: info.clashUrl, v2ray: info.v2rayUrl, count: info.slots.healthy },
    { key: 'stable', label: t('subscription.presetStableLabel'), hint: t('subscription.presetStableHint'), clash: subscriptionQuery(info.clashUrl, presetOptions('stable')), v2ray: subscriptionQuery(info.v2rayUrl, presetOptions('stable')), count: Math.min(presetLimit, info.slots.healthy) },
    { key: 'quality', label: t('subscription.presetQualityLabel'), hint: t('subscription.presetQualityHint'), clash: subscriptionQuery(info.clashUrl, presetOptions('quality')), v2ray: subscriptionQuery(info.v2rayUrl, presetOptions('quality')), count: Math.min(presetLimit, info.slots.healthy) },
  ].filter(preset => preset.clash)
  const countryLinks = Object.entries(info.countries)
    .sort(([leftCode, leftCount], [rightCode, rightCount]) => rightCount - leftCount || leftCode.localeCompare(rightCode))
    .map(([code, count]) => ({
      code,
      count,
      pool: info.poolCountries[code] ?? 0,
      clash: subscriptionQuery(info.clashUrl, { countries: code }),
      v2ray: subscriptionQuery(info.v2rayUrl, { countries: code }),
    }))
  const visibleCountries = showAllCountries ? countryLinks : countryLinks.slice(0, 8)
  const qrValues = [
    { key: 'v2ray', label: t('subscription.v2rayLabel'), value: info.v2rayUrl },
    { key: 'clash', label: t('subscription.clashLabel'), value: info.clashUrl },
    { key: 'auto', label: t('subscription.autoLabel'), value: info.autoUrl },
  ].filter(entry => entry.value)
  return <>
    <SettingsSection index="01" title={t('subscription.linksTitle')} description={t('subscription.linksDescription')}>
      <LinkField fieldKey="v2ray" label={t('subscription.v2rayLabel')} hint={t('subscription.v2rayHint')} value={info.v2rayUrl} copied={copied} copy={copy} />
      <LinkField fieldKey="clash" label={t('subscription.clashLabel')} hint={t('subscription.clashHint')} value={info.clashUrl} copied={copied} copy={copy} />
      <LinkField fieldKey="auto" label={t('subscription.autoLabel')} hint={t('subscription.autoHint')} value={info.autoUrl} copied={copied} copy={copy} />
      <LinkField fieldKey="socksb64" label={t('share.subSocksB64')} hint={t('share.subSocksB64Hint')} value={subscriptionBase ? `${subscriptionBase}/socksb64` : ''} copied={copied} copy={copy} />
      <LinkField fieldKey="socks5plain" label={t('share.subSocksPlain')} hint={t('share.subSocksPlainHint')} value={subscriptionBase ? `${subscriptionBase}/socks5` : ''} copied={copied} copy={copy} />
    </SettingsSection>
    <SettingsSection index="02" title={t('subscription.presetsTitle')} description={t('subscription.presetsDescription')}>
      <div className="preset-grid wide">
        {presets.map(preset => <article className="preset-card" key={preset.key}>
          <div className="preset-card-head"><strong>{preset.label}</strong><small>{t('subscription.presetNodes', { count: preset.count })}</small></div>
          <p>{preset.hint}</p>
          <div className="preset-actions">
            <button type="button" className="quiet-button" onClick={() => copy(`preset-${preset.key}-clash`, preset.clash)}>{copied === `preset-${preset.key}-clash` ? t('subscription.copied') : t('subscription.copyClash')}</button>
            <button type="button" className="quiet-button" onClick={() => copy(`preset-${preset.key}-v2ray`, preset.v2ray)}>{copied === `preset-${preset.key}-v2ray` ? t('subscription.copied') : t('subscription.copyV2ray')}</button>
          </div>
        </article>)}
      </div>
    </SettingsSection>
    {countryLinks.length > 0 && <SettingsSection index="03" title={t('subscription.countriesTitle')} description={t('subscription.countriesDescription', { count: countryLinks.length })}>
      <div className="country-link-grid wide">
        {visibleCountries.map(entry => <article className="country-link-card" key={entry.code}>
          <div className="country-link-head"><strong>{countryFlag(entry.code)} {entry.code}</strong><small>{t('subscription.countryNodes', { count: entry.count })}{entry.pool > 0 ? ` · ${t('subscription.countryPool', { count: entry.pool })}` : ''}</small></div>
          <div className="preset-actions">
            <button type="button" className="quiet-button" onClick={() => copy(`country-${entry.code}-clash`, entry.clash)}>{copied === `country-${entry.code}-clash` ? t('subscription.copied') : t('subscription.copyClash')}</button>
            <button type="button" className="quiet-button" onClick={() => copy(`country-${entry.code}-v2ray`, entry.v2ray)}>{copied === `country-${entry.code}-v2ray` ? t('subscription.copied') : t('subscription.copyV2ray')}</button>
          </div>
        </article>)}
      </div>
      {countryLinks.length > 8 && <button type="button" className="quiet-button country-toggle" onClick={() => setShowAllCountries(value => !value)}>{showAllCountries ? t('subscription.countriesShowLess') : t('subscription.countriesShowAll', { count: countryLinks.length })}</button>}
    </SettingsSection>}
    {qrValues.length > 0 && <SettingsSection index="04" title={t('subscription.qrTitle')} description={t('subscription.qrHint')}>
      <div className="qr-grid wide">
        {qrValues.map(entry => <article className="qr-card" key={entry.key}>
          <QRCodeSVG value={entry.value} size={132} level="M" marginSize={2} bgColor="#ffffff" fgColor="#111827" />
          <strong>{entry.label}</strong>
          <button type="button" className="quiet-button" onClick={() => copy(`qr-${entry.key}`, entry.value)}>{copied === `qr-${entry.key}` ? t('subscription.copied') : t('subscription.copy')}</button>
        </article>)}
      </div>
    </SettingsSection>}
    <SettingsSection index="05" title={t('subscription.detailTitle')} description={t('subscription.detailDescription')}>
      <Field label={t('subscription.protocol')}><input readOnly value={`Trojan · WebSocket · TLS${info.udp ? ' · UDP' : ''}`} /></Field>
      <Field label={t('subscription.wsPath')}><input readOnly value={info.trojanPath} /></Field>
      <Field label={t('subscription.publicHost')}><input readOnly value={info.publicHost || '—'} /></Field>
      <Field label={t('subscription.token')}><input readOnly value={info.token} onFocus={event => event.currentTarget.select()} /></Field>
    </SettingsSection>
    <section className="status-grid">
      <article className="status-card"><div className="status-card-head"><span>{t('subscription.slotsReady')}</span><small>{t('subscription.slotsTotal', { count: info.slots.total })}</small></div><strong>{info.slots.ready}</strong><p>{t('subscription.readyHint')}</p></article>
      <article className="status-card"><div className="status-card-head"><span>{t('subscription.slotsSwitching')}</span><i /></div><strong>{info.slots.switching}</strong><p>{t('subscription.switchingHint')}</p></article>
      <article className="status-card"><div className="status-card-head"><span>{t('subscription.slotsError')}</span></div><strong>{info.slots.error}</strong><p>{t('subscription.errorHint')}</p></article>
      <article className="status-card"><div className="status-card-head"><span>{t('subscription.nodesInProfile')}</span><small>{t('subscription.nodesUnit')}</small></div><strong>{info.slots.healthy}</strong><p>{t('subscription.nodesHint')}</p></article>
    </section>
  </>
}

type SubscriptionOptions = { count: number; countries: string[]; exclude: string[]; slots: number[]; shuffle: boolean; group: boolean; sort: string }

function buildSubscriptionUrl(base: string, options: SubscriptionOptions) {
  const params: Record<string, string> = {}
  if (options.count > 0) params.count = String(options.count)
  if (options.countries.length > 0) params.countries = options.countries.join(',')
  if (options.exclude.length > 0) params.exclude = options.exclude.join(',')
  if (options.slots.length > 0) params.slots = options.slots.join(',')
  if (options.shuffle) params.shuffle = '1'
  if (options.group) params.group = 'country'
  if (options.sort) params.sort = options.sort
  return subscriptionQuery(base, params)
}

function subscriptionQuery(base: string, params: Record<string, string>) {
  if (!base) return ''
  const query = new URLSearchParams(params).toString()
  if (!query) return base
  return `${base}${base.includes('?') ? '&' : '?'}${query}`
}

type CountryOption = { code: string; active: number; pool: number }

function CountryPicker({ title, hint, selected, options, exclude, onChange }: {
  title: string
  hint: string
  selected: string[]
  options: CountryOption[]
  exclude?: boolean
  onChange: (next: string[]) => void
}) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const root = useRef<HTMLDivElement>(null)
  const selectedSet = useMemo(() => new Set(selected), [selected])
  useEffect(() => {
    if (!open) return
    const close = (event: MouseEvent) => {
      if (root.current && !root.current.contains(event.target as HTMLElement)) setOpen(false)
    }
    document.addEventListener('mousedown', close)
    return () => document.removeEventListener('mousedown', close)
  }, [open])
  const needle = query.trim().toLowerCase()
  const filtered = needle ? options.filter(option => option.code.toLowerCase().includes(needle)) : options
  const toggle = (code: string) => onChange(selectedSet.has(code) ? selected.filter(item => item !== code) : [...selected, code])
  return <div className={`country-picker${exclude ? ' exclude' : ''}`} ref={root}>
    <div className="country-picker-head"><strong>{title}</strong><small>{hint}</small><button type="button" className="quiet-button" disabled={selected.length === 0} onClick={() => onChange([])}>{t('subscription.builderClear')}</button></div>
    <button type="button" className="country-trigger" aria-expanded={open} onClick={() => setOpen(current => !current)}>
      <span>{selected.length === 0 ? t('subscription.builderCountryAll') : selected.map(code => `${countryFlag(code)} ${code}`).join(' · ')}</span>
      <small>{t('subscription.builderCountryChosen', { count: selected.length })}</small>
    </button>
    {open && <div className="country-menu">
      <input className="country-search" autoFocus placeholder={t('subscription.builderCountrySearch')} value={query} onChange={event => setQuery(event.target.value)} />
      <div className="country-menu-list">
        {filtered.length === 0 ? <p className="muted-line">{t('subscription.builderCountryEmpty')}</p> : filtered.map(option => <label key={option.code} className="country-option">
          <input type="checkbox" checked={selectedSet.has(option.code)} onChange={() => toggle(option.code)} />
          <span className="country-option-name"><span className="country-mark small">{countryFlag(option.code)}</span><span className="mono">{option.code}</span></span>
          <small>{t('subscription.builderCountryCount', { active: option.active, pool: option.pool })}</small>
        </label>)}
      </div>
    </div>}
    {selected.length > 0 && <div className="country-selected">{selected.map(code => <button type="button" key={code} className={`chip${exclude ? ' exclude' : ''} active`} onClick={() => toggle(code)}>{countryFlag(code)} {code} ×</button>)}</div>}
  </div>
}

function SubscriptionBuilder({ info, copy, copied, onOpenPool }: { info: SubscriptionInfo; copy: (key: string, value: string) => void; copied: string; onOpenPool?: () => void }) {
  const { t } = useTranslation()
  const [count, setCount] = useState(0)
  const [countries, setCountries] = useState<string[]>([])
  const [exclude, setExclude] = useState<string[]>([])
  const [slots, setSlots] = useState<number[]>([])
  const [slotCountry, setSlotCountry] = useState('')
  const [slotQuery, setSlotQuery] = useState('')
  const [shuffle, setShuffle] = useState(false)
  const [group, setGroup] = useState(false)
  const [sort, setSort] = useState('')

  const commonCountries = ['US', 'JP', 'KR', 'DE', 'GB', 'FR', 'NL', 'CA', 'AU', 'SG', 'HK', 'TW', 'IN', 'RU', 'BR', 'TR', 'IT', 'ES', 'PL', 'SE', 'CH', 'AE', 'VN', 'TH', 'MY', 'ID', 'PH', 'MX', 'ZA', 'AR']
  const selectedSlots = useMemo(() => new Set(slots), [slots])
  const slotOptions = useMemo(() => {
    const needle = slotQuery.trim().toLowerCase()
    return info.nodes.filter(node => {
      if (slotCountry && node.country !== slotCountry) return false
      if (!needle) return true
      return [String(node.slotId), node.name, node.country ?? '', node.source ?? '', node.asnOrg ?? ''].some(value => value.toLowerCase().includes(needle))
    }).sort((left, right) => left.slotId - right.slotId)
  }, [info.nodes, slotCountry, slotQuery])
  const toggleSlot = (id: number) => setSlots(current => current.includes(id) ? current.filter(value => value !== id) : [...current, id])

  const allCountries = useMemo(() => {
    const map = new Map<string, CountryOption>()
    for (const [code, total] of Object.entries(info.poolCountries ?? {})) {
      map.set(code, { code, active: 0, pool: total })
    }
    for (const [code, total] of Object.entries(info.countries)) {
      const existing = map.get(code)
      map.set(code, { code, active: total, pool: existing?.pool ?? total })
    }
    for (const node of info.nodes) {
      if (node.country && !map.has(node.country)) {
        map.set(node.country, { code: node.country, active: 0, pool: 0 })
      }
    }
    return Array.from(map.values()).sort((left, right) => right.pool - left.pool || right.active - left.active || left.code.localeCompare(right.code))
  }, [info])
  const missingCountries = countries.filter(code => !info.countries[code])
  const effectiveCount = slots.length > 0 ? 0 : count
  const options: SubscriptionOptions = { count: effectiveCount, countries, exclude, slots, shuffle, group, sort }
  const v2rayUrl = buildSubscriptionUrl(info.v2rayUrl, options)
  const clashUrl = buildSubscriptionUrl(info.clashUrl, options)
  const available = useMemo(() => info.nodes.filter(node => {
    if (slots.length > 0 && !selectedSlots.has(node.slotId)) return false
    if (node.country && exclude.includes(node.country)) return false
    if (countries.length === 0) return true
    return Boolean(node.country && countries.includes(node.country))
  }), [info.nodes, countries, exclude, slots, selectedSlots])
  const preview = effectiveCount > 0 ? available.slice(0, effectiveCount) : available

  return <>
    <SettingsSection index="01" title={t('subscription.builderTitle')} description={t('subscription.builderDescription')}>
      <Field label={t('subscription.builderCount')} hint={t('subscription.builderCountHint')}><input type="number" min={0} max={info.nodes.length} value={count} onChange={event => setCount(Math.max(0, Number(event.target.value) || 0))} /></Field>
      <Field label={t('subscription.builderSort')} hint={t('subscription.builderSortHint')}><select value={sort} onChange={event => setSort(event.target.value)}><option value="">{t('subscription.builderSortDefault')}</option><option value="country">{t('subscription.builderSortCountry')}</option><option value="name">{t('subscription.builderSortName')}</option></select></Field>
      <label className="toggle-field"><input type="checkbox" checked={shuffle} onChange={event => setShuffle(event.target.checked)} /><span><strong>{t('subscription.builderShuffle')}</strong><small>{t('subscription.builderShuffleHint')}</small></span></label>
      <label className="toggle-field"><input type="checkbox" checked={group} onChange={event => setGroup(event.target.checked)} /><span><strong>{t('subscription.builderGroup')}</strong><small>{t('subscription.builderGroupHint')}</small></span></label>
      <div className="wide common-countries">
        <span>{t('subscription.builderCommonCountries')}</span>
        <div className="chip-row">{commonCountries.map(code => <button type="button" key={code} className={`chip${countries.includes(code) ? ' active' : ''}`} onClick={() => setCountries(current => current.includes(code) ? current.filter(value => value !== code) : [...current, code])}>{countryFlag(code)} {code}</button>)}</div>
      </div>
      <CountryPicker title={t('subscription.builderCountries')} hint={t('subscription.builderCountriesHint')} selected={countries} options={allCountries} onChange={setCountries} />
      <CountryPicker title={t('subscription.builderExclude')} hint={t('subscription.builderExcludeHint')} selected={exclude} options={allCountries} exclude onChange={setExclude} />
      <div className="wide builder-filter-summary">
        <span>{t('subscription.builderFilterSummary', { include: countries.length, exclude: exclude.length, available: available.length })}</span>
        {missingCountries.length > 0 && <span className="warning-text">{t('subscription.builderMissingSlotsShort', { countries: missingCountries.join(', ') })}</span>}
      </div>
      {missingCountries.length > 0 && <div className="wide builder-warning">
        <Notice tone="warning">{t('subscription.builderMissingSlots', { countries: missingCountries.join(', ') })}</Notice>
        {onOpenPool && <button type="button" className="quiet-button" onClick={onOpenPool}>{t('subscription.builderGoPool')}</button>}
      </div>}
    </SettingsSection>
    <SettingsSection index="02" title={t('subscription.builderSlots')} description={t('subscription.builderSlotsHint')}>
      <div className="wide slot-picker">
        <div className="slot-picker-head">
          <input className="country-search" placeholder={t('subscription.builderSlotSearch')} value={slotQuery} onChange={event => setSlotQuery(event.target.value)} />
          <select value={slotCountry} onChange={event => setSlotCountry(event.target.value)}>
            <option value="">{t('nodes.filterAll')}</option>
            {allCountries.map(option => <option key={option.code} value={option.code}>{countryFlag(option.code)} {option.code} ({option.active})</option>)}
          </select>
          <button type="button" className="quiet-button" onClick={() => setSlots(slotOptions.map(node => node.slotId))}>{t('nodes.batchSelectAll', { count: slotOptions.length })}</button>
          <button type="button" className="quiet-button" disabled={slots.length === 0} onClick={() => setSlots([])}>{t('subscription.builderClear')}</button>
          <small>{t('subscription.builderSlotChosen', { count: slots.length })}</small>
        </div>
        {slotOptions.length === 0 ? <p className="muted-line">{t('subscription.builderEmpty')}</p> : <div className="slot-list">
          {slotOptions.map(node => <label key={node.slotId} className={`slot-option${selectedSlots.has(node.slotId) ? ' active' : ''}`}>
            <input type="checkbox" checked={selectedSlots.has(node.slotId)} onChange={() => toggleSlot(node.slotId)} />
            <span className="country-mark small">{countryFlag(node.country || '')}</span>
            <span className="slot-option-name" title={node.asnOrg || node.name}>{node.name}</span>
            <small className="mono">#{node.slotId} {node.country || '—'}</small>
            {node.source && <em className="mini-badge">{node.source}</em>}
            <small>{node.protocol ? protocolLabels[node.protocol] : '—'} · {formatRate(node.speedBps)} · {node.pingMs > 0 ? `${node.pingMs} ms` : '—'} · Q{Math.round(node.qualityScore)}</small>
          </label>)}
        </div>}
      </div>
    </SettingsSection>
    <SettingsSection index="03" title={t('subscription.builderPreview')} description={t('subscription.builderPreviewHint')}>
      <div className="builder-preview wide">
        <div className="builder-preview-head"><strong>{t('subscription.builderPreviewCount', { count: preview.length, total: info.nodes.length })}</strong>{shuffle && <span className="mini-badge">{t('subscription.builderShuffled')}</span>}</div>
        {preview.length === 0 ? <p className="muted-line">{t('subscription.builderEmpty')}</p> : <div className="preview-list">{preview.slice(0, 60).map(node => <div className="preview-item" key={node.slotId}><span>{countryFlag(node.country || '')}</span><span title={node.asnOrg}>{node.name}</span>{node.chained && <em>{t('subscription.chained')}</em>}</div>)}</div>}
        {preview.length > 60 && <small>{t('subscription.builderMore', { count: preview.length - 60 })}</small>}
      </div>
    </SettingsSection>
    <SettingsSection index="04" title={t('subscription.builderGenerate')} description={t('subscription.builderGenerateHint')}>
      <LinkField fieldKey="builder-v2ray" label={t('subscription.v2rayLabel')} hint={t('subscription.builderUrlHint')} value={v2rayUrl} copied={copied} copy={copy} />
      <LinkField fieldKey="builder-clash" label={t('subscription.clashLabel')} hint={t('subscription.builderUrlHint')} value={clashUrl} copied={copied} copy={copy} />
      <p className="section-note wide">{t('subscription.builderNote')}</p>
    </SettingsSection>
  </>
}

function MatrixPool({ onChanged }: { onChanged: () => void }) {
  const { t } = useTranslation()
  const [info, setInfo] = useState<MatrixSlotsInfo>()
  const [error, setError] = useState('')
  const [note, setNote] = useState('')
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [busySlot, setBusySlot] = useState(0)
  const [country, setCountry] = useState('')
  const [from, setFrom] = useState(1)
  const [count, setCount] = useState(10)
  const [page, setPage] = useState(1)

  const load = useCallback(async (silent = false) => {
    if (!silent) setLoading(true)
    try {
      const value = await api.get<MatrixSlotsInfo>('/api/matrix/slots')
      setInfo(value)
      setError('')
    } catch (reason) {
      setError(errorMessage(reason))
    } finally {
      if (!silent) setLoading(false)
    }
  }, [])

  useEffect(() => { void load() }, [load])
  useEffect(() => {
    const timer = window.setInterval(() => { void load(true) }, 6000)
    return () => window.clearInterval(timer)
  }, [load])

  const countryOptions = useMemo(() => {
    const options = new Map<string, number>()
    for (const entry of info?.countries ?? []) options.set(entry.code, entry.total)
    for (const slot of info?.slots ?? []) {
      if (slot.country && !options.has(slot.country)) options.set(slot.country, 0)
    }
    return Array.from(options.entries()).sort((left, right) => right[1] - left[1] || left[0].localeCompare(right[0]))
  }, [info])

  const configure = async (slot: MatrixSlot, code: string) => {
    setBusySlot(slot.slotId)
    setError('')
    setNote('')
    try {
      await api.post(`/api/matrix/slots/${slot.slotId}/configure`, { country: code, protocol: slot.protocol || '', mode: slot.mode || 'sticky' })
      setNote(t('subscription.pool.applied', { updated: 1, failed: 0 }))
      load()
      onChanged()
    } catch (reason) {
      setError(errorMessage(reason))
    } finally {
      setBusySlot(0)
    }
  }

  const rotate = async (slot: MatrixSlot) => {
    setBusySlot(slot.slotId)
    setError('')
    try {
      await api.post(`/api/matrix/slots/${slot.slotId}/rotate`)
      load()
    } catch (reason) {
      setError(errorMessage(reason))
    } finally {
      setBusySlot(0)
    }
  }

  const applyBulk = async () => {
    setSaving(true)
    setError('')
    setNote('')
    try {
      const result = await api.post<{ updated: number; failed: number }>('/api/matrix/slots/country', { country, protocol: '', mode: 'sticky', from, count })
      setNote(t('subscription.pool.applied', { updated: result.updated, failed: result.failed }))
      load()
      onChanged()
    } catch (reason) {
      setError(errorMessage(reason))
    } finally {
      setSaving(false)
    }
  }

  const slots = info?.slots ?? []
  const pageSize = 12
  const pageCount = Math.max(1, Math.ceil(slots.length / pageSize))
  const currentPage = Math.min(page, pageCount)
  const pageSlots = slots.slice((currentPage - 1) * pageSize, currentPage * pageSize)

  return <>
    {error && <Notice tone="danger">{error}</Notice>}
    {note && <Notice tone="success">{note}</Notice>}
    <SettingsSection stacked index="01" title={t('subscription.pool.title')} description={t('subscription.pool.description')}>
      <div className="wide pool-toolbar">
        <div className="pool-toolbar-head">
          <p className="muted-line">{t('subscription.pool.summary', { slots: slots.length, healthy: info?.healthy ?? 0, max: info?.maxSlots ?? 0 })}</p>
          <div className="pool-actions">
            <button type="button" className="primary-button" disabled={saving || !info} onClick={() => void applyBulk()}>{saving ? <Spinner /> : null}{t('subscription.pool.apply')}</button>
            <button type="button" className="quiet-button" disabled={loading} onClick={() => void load()}>{loading ? <Spinner /> : <RefreshIcon />}{t('subscription.pool.refresh')}</button>
          </div>
        </div>
        <div className="pool-toolbar-fields">
          <Field label={t('subscription.pool.country')} hint={t('subscription.pool.bulkHint')}>
            <select value={country} onChange={event => setCountry(event.target.value)}>
              <option value="">{t('subscription.pool.any')}</option>
              {countryOptions.map(([code, total]) => <option key={code} value={code}>{countryFlag(code)} {code} ({total})</option>)}
            </select>
          </Field>
          <Field label={t('subscription.pool.from')}>
            <input type="number" min={1} max={info?.maxSlots ?? 1} value={from} onChange={event => setFrom(Math.max(1, Number(event.target.value) || 1))} />
          </Field>
          <Field label={t('subscription.pool.count')}>
            <input type="number" min={1} max={info?.maxSlots ?? 1} value={count} onChange={event => setCount(Math.max(1, Number(event.target.value) || 1))} />
          </Field>
        </div>
      </div>
      {slots.length === 0 ? <p className="muted-line wide">{t('subscription.pool.empty')}</p> : <div className="node-table-wrap wide">
        <table className="node-table pool-table">
          <thead><tr>
            <th>{t('subscription.pool.slot')}</th>
            <th>{t('subscription.pool.country')}</th>
            <th>{t('subscription.pool.actual')}</th>
            <th>{t('subscription.pool.exit')}</th>
            <th>{t('subscription.pool.state')}</th>
            <th>{t('subscription.pool.health')}</th>
            <th>{t('subscription.pool.quality')}</th>
            <th>{t('subscription.pool.source')}</th>
            <th />
          </tr></thead>
          <tbody>
            {pageSlots.map(slot => <tr key={slot.slotId}>
              <td className="mono">{slot.username}<br /><small>:{slot.port}</small></td>
              <td><select disabled={busySlot === slot.slotId} value={slot.country || ''} onChange={event => configure(slot, event.target.value)}>
                <option value="">{t('subscription.pool.any')}</option>
                {countryOptions.map(([code, total]) => <option key={code} value={code}>{countryFlag(code)} {code} ({total})</option>)}
              </select></td>
              <td>{slot.countryShort ? <><span className="country-mark small">{countryFlag(slot.countryShort)}</span> <span className="mono">{slot.countryShort}</span>{slot.asnOrg ? <small title={slot.asnOrg}> · {slot.asnOrg}</small> : null}</> : '—'}</td>
              <td className="mono">{slot.exitIp || '—'}</td>
              <td>{slot.state}</td>
              <td>{slot.state === 'ready' ? (slot.healthy ? <span className="live-badge">{t('subscription.pool.healthy')}</span> : <span className="pending-badge">{t('subscription.pool.unhealthy')}</span>) : <span className="pending-badge">{slot.state}</span>}</td>
              <td className="mono">{slot.qualityScore ? Math.round(slot.qualityScore) : '—'}{slot.cooldownUntil ? <small title={slot.cooldownUntil}> · C</small> : null}<br /><small>{formatRate(slot.speedBps)} · {slot.pingMs > 0 ? `${slot.pingMs} ms` : '—'}</small></td>
              <td>{slot.source ? <span className="mini-badge" title={slot.source}>{slot.source}</span> : '—'}</td>
              <td><button type="button" className="quiet-button" disabled={busySlot === slot.slotId} onClick={() => rotate(slot)}>{t('subscription.pool.rotate')}</button></td>
            </tr>)}
          </tbody>
        </table>
      </div>}
      {pageCount > 1 && <div className="wide"><Pagination page={currentPage} pageCount={pageCount} onChange={setPage} /></div>}
    </SettingsSection>
  </>
}

type ClientDraft = {
  name: string
  token: string
  count: number
  countries: string
  exclude: string
  shuffle: boolean
  group: string
  sort: string
}

function randomClientToken() {
  const alphabet = 'abcdefghijklmnopqrstuvwxyz0123456789'
  const bytes = new Uint8Array(24)
  crypto.getRandomValues(bytes)
  return Array.from(bytes, byte => alphabet[byte % alphabet.length]).join('')
}

function clientDraftFrom(client: SubscriptionClientInfo): ClientDraft {
  return {
    name: client.name,
    token: client.token,
    count: client.count,
    countries: (client.countries ?? []).join(','),
    exclude: (client.exclude ?? []).join(','),
    shuffle: client.shuffle,
    group: client.group || 'country',
    sort: client.sort,
  }
}

function splitCountryCodes(raw: string) {
  return raw.split(/[\s,;|]+/).map(code => code.trim().toUpperCase()).filter(code => code.length === 2)
}

function SubscriptionClients({ info, copy, copied, onChanged }: { info: SubscriptionInfo; copy: (key: string, value: string) => void; copied: string; onChanged: () => void }) {
  const { t } = useTranslation()
  const [drafts, setDrafts] = useState<ClientDraft[]>(() => info.clients.map(clientDraftFrom))
  const [saving, setSaving] = useState(false)
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')

  useEffect(() => {
    setDrafts(info.clients.map(clientDraftFrom))
  }, [info.clients])

  const savedByToken = useMemo(() => new Map(info.clients.map(client => [client.token, client])), [info.clients])
  const update = (index: number, patch: Partial<ClientDraft>) => setDrafts(current => current.map((draft, position) => position === index ? { ...draft, ...patch } : draft))
  const add = () => setDrafts(current => [...current, { name: `client-${current.length + 1}`, token: randomClientToken(), count: 0, countries: '', exclude: '', shuffle: false, group: 'country', sort: '' }])
  const remove = (index: number) => setDrafts(current => current.filter((_, position) => position !== index))

  const save = async () => {
    setSaving(true)
    setError('')
    setMessage('')
    try {
      await api.put('/api/subscription/clients', {
        clients: drafts.map(draft => ({
          name: draft.name.trim(),
          token: draft.token.trim(),
          count: Math.max(0, draft.count),
          countries: splitCountryCodes(draft.countries),
          exclude: splitCountryCodes(draft.exclude),
          shuffle: draft.shuffle,
          group: draft.group,
          sort: draft.sort,
        })),
      })
      setMessage(t('subscription.clientSaved'))
      onChanged()
    } catch (reason) {
      setError(errorMessage(reason))
    } finally {
      setSaving(false)
    }
  }

  return <>
    <SettingsSection index="01" title={t('subscription.clientsTitle')} description={t('subscription.clientsDescription')}>
      {drafts.length === 0 && <p className="muted-line wide">{t('subscription.clientEmpty')}</p>}
      <div className="client-grid wide">
        {drafts.map((draft, index) => {
          const saved = savedByToken.get(draft.token.trim())
          return <article className="client-card" key={`${draft.token}-${index}`}>
            <div className="client-card-head">
              <strong>{draft.name || t('subscription.clientName')}</strong>
              <button type="button" className="quiet-button danger" onClick={() => remove(index)}>{t('subscription.clientRemove')}</button>
            </div>
            <Field label={t('subscription.clientName')}><input value={draft.name} onChange={event => update(index, { name: event.target.value })} /></Field>
            <Field label={t('subscription.clientToken')}>
              <div className="link-copy">
                <input value={draft.token} onChange={event => update(index, { token: event.target.value })} />
                <button type="button" className="quiet-button" onClick={() => update(index, { token: randomClientToken() })}>{t('subscription.clientGenerate')}</button>
              </div>
            </Field>
            <div className="client-card-row">
              <Field label={t('subscription.clientCount')}><input type="number" min={0} max={256} value={draft.count} onChange={event => update(index, { count: Math.max(0, Number(event.target.value) || 0) })} /></Field>
              <Field label={t('subscription.clientGroup')}><select value={draft.group} onChange={event => update(index, { group: event.target.value })}><option value="country">{t('subscription.builderGroup')}</option><option value="none">{t('subscription.clientDefault')}</option></select></Field>
              <Field label={t('subscription.clientSort')}><select value={draft.sort} onChange={event => update(index, { sort: event.target.value })}><option value="">{t('subscription.builderSortDefault')}</option><option value="country">{t('subscription.builderSortCountry')}</option><option value="name">{t('subscription.builderSortName')}</option></select></Field>
            </div>
            <div className="client-card-row">
              <Field label={t('subscription.clientCountries')}><input placeholder="JP,KR" value={draft.countries} onChange={event => update(index, { countries: event.target.value })} /></Field>
              <Field label={t('subscription.clientExclude')}><input placeholder="US,RU" value={draft.exclude} onChange={event => update(index, { exclude: event.target.value })} /></Field>
            </div>
            <label className="toggle-field compact"><input type="checkbox" checked={draft.shuffle} onChange={event => update(index, { shuffle: event.target.checked })} /><span><strong>{t('subscription.clientShuffle')}</strong><small>{t('subscription.builderShuffleHint')}</small></span></label>
            {saved && saved.v2rayUrl && <div className="client-urls">
              <div className="client-qr"><QRCodeSVG value={saved.autoUrl || saved.v2rayUrl} size={104} level="M" marginSize={2} bgColor="#ffffff" fgColor="#111827" /></div>
              <div className="client-url-list">
                <LinkField fieldKey={`client-${index}-v2ray`} label={t('subscription.v2rayLabel')} hint={t('subscription.clientUrls')} value={saved.v2rayUrl} copied={copied} copy={copy} />
                <LinkField fieldKey={`client-${index}-clash`} label={t('subscription.clashLabel')} hint={t('subscription.clientUrls')} value={saved.clashUrl} copied={copied} copy={copy} />
              </div>
            </div>}
          </article>
        })}
      </div>
      <p className="section-note wide">{t('subscription.clientHint')}</p>
    </SettingsSection>
    {error && <Notice tone="danger">{error}</Notice>}
    <div className="settings-actions">
      <div><strong>{message || t('subscription.clientsTitle')}</strong><small>{t('subscription.clientsDescription')}</small></div>
      <div className="client-actions">
        <button className="quiet-button" type="button" onClick={add}>{t('subscription.clientAdd')}</button>
        <button className="primary-button" type="button" disabled={saving} onClick={save}>{saving ? <Spinner /> : null}{t('subscription.clientSave')}</button>
      </div>
    </div>
  </>
}

function FrontProxyPanel({ initial, onSaved }: { initial: FrontProxySettings; onSaved: () => void }) {
  const { t } = useTranslation()
  const [settings, setSettings] = useState<FrontProxySettings>(initial)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')

  useEffect(() => { setSettings(initial) }, [initial])

  const update = <Key extends keyof FrontProxySettings>(key: Key, value: FrontProxySettings[Key]) => setSettings(current => ({ ...current, [key]: value }))
  const needsURL = ['socks5', 'socks5h', 'http', 'https', 'list'].includes(settings.type)

  const submit = async (event: FormEvent) => {
    event.preventDefault(); setError(''); setMessage(''); setSaving(true)
    try {
      const saved = await api.put<FrontProxySettings>('/api/matrix/front-proxy', settings)
      setSettings(saved)
      setMessage(t('subscription.front.saved'))
      onSaved()
    } catch (reason) {
      setError(errorMessage(reason))
    } finally {
      setSaving(false)
    }
  }

  return <form onSubmit={submit}>
    <SettingsSection index="01" title={t('subscription.front.title')} description={t('subscription.front.description')}>
      <label className="toggle-field wide"><input type="checkbox" checked={settings.enabled} onChange={event => update('enabled', event.target.checked)} /><span><strong>{t('subscription.front.enable')}</strong><small>{t('subscription.front.enableHint')}</small></span></label>
      <Field label={t('subscription.front.type')} hint={t('subscription.front.typeHint')}>
        <select value={settings.type} onChange={event => update('type', event.target.value)}>
          <option value="socks5">SOCKS5</option>
          <option value="socks5h">SOCKS5 (remote DNS)</option>
          <option value="http">HTTP CONNECT</option>
          <option value="https">HTTPS CONNECT</option>
          <option value="list">{t('subscription.front.typeList')}</option>
          <option value="slot">{t('subscription.front.typeSlot')}</option>
          <option value="api">{t('subscription.front.typeApi')}</option>
        </select>
      </Field>
      {needsURL && <Field label={t('subscription.front.url')} hint={t('subscription.front.urlHint')} wide><input placeholder="socks5://user:pass@HOST:1080" value={settings.url} onChange={event => update('url', event.target.value)} /></Field>}
      {settings.type === 'slot' && <Field label={t('subscription.front.slot')} hint={t('subscription.front.slotHint')}><input type="number" min={1} value={settings.slot} onChange={event => update('slot', Number(event.target.value) || 0)} /></Field>}
      {settings.type === 'api' && <Field label={t('subscription.front.api')} hint={t('subscription.front.apiHint')} wide><input placeholder="https://API/proxies" value={settings.api} onChange={event => update('api', event.target.value)} /></Field>}
      <Field label={t('subscription.front.timeout')} hint={t('subscription.front.timeoutHint')}><input placeholder="15s" value={settings.timeout} onChange={event => update('timeout', event.target.value)} /></Field>
      <p className="section-note wide">{t('subscription.front.compatHint')}</p>
    </SettingsSection>
    {error && <Notice tone="danger">{error}</Notice>}
    {message && <Notice tone="success">{message}</Notice>}
    <div className="settings-actions"><div><strong>{t('subscription.front.applies')}</strong><small>{t('subscription.front.appliesHint')}</small></div><button className="primary-button" disabled={saving}>{saving ? <Spinner /> : null}{t('subscription.front.save')}</button></div>
  </form>
}

type SettingsTab = 'source' | 'protocol' | 'entry' | 'probe' | 'system'

const settingsTabs: Array<{ id: SettingsTab; index: string; label: string }> = [
  { id: 'source', index: '01', label: 'settings.tabSource' },
  { id: 'protocol', index: '02', label: 'settings.tabProtocol' },
  { id: 'entry', index: '03', label: 'settings.tabEntry' },
  { id: 'probe', index: '04', label: 'settings.tabProbe' },
  { id: 'system', index: '05', label: 'settings.tabSystem' },
]

function Settings() {
  const { t } = useTranslation()
  const [settings, setSettings] = useState<Config>()
  const [newPassword, setNewPassword] = useState('')
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [restartRequired, setRestartRequired] = useState(false)
  const [saving, setSaving] = useState(false)
  const [restarting, setRestarting] = useState(false)
  const [sourceTab, setSourceTab] = useState<'basic' | 'filter'>('basic')
  const [settingsTab, setSettingsTab] = useState<SettingsTab>('source')

  const load = useCallback(async () => {
    try { setSettings(await api.get<Config>('/api/config')); setError('') }
    catch (reason) { setError(errorMessage(reason)) }
  }, [])
  useEffect(() => { void load() }, [load])

  const update = <Key extends keyof Config>(key: Key, value: Config[Key]) => setSettings(current => current ? { ...current, [key]: value } : current)
  const moveProtocol = (index: number, offset: number) => {
    if (!settings) return
    const target = index + offset
    if (target < 0 || target >= settings.protocolPriority.length) return
    const protocols = [...settings.protocolPriority]
    ;[protocols[index], protocols[target]] = [protocols[target], protocols[index]]
    update('protocolPriority', protocols)
  }

  const submit = async (event: FormEvent) => {
    event.preventDefault(); setError(''); setMessage('')
    if (!settings) return
    setSaving(true)
    try {
      const result = await api.put<{ restartRequired: boolean }>('/api/config', { config: settings, newPassword })
      setRestartRequired(result.restartRequired)
      setMessage(result.restartRequired ? t('settings.savedRestart') : t('settings.saved'))
      setNewPassword('')
      await load()
    } catch (reason) {
      setError(errorMessage(reason))
    } finally {
      setSaving(false)
    }
  }

  const restart = async () => {
    if (!window.confirm(t('settings.restartConfirm'))) return
    setError(''); setMessage(t('settings.restarting')); setRestarting(true)
    try {
      await api.post('/api/restart')
      window.setTimeout(() => window.location.reload(), 2_500)
    } catch (reason) {
      setError(errorMessage(reason)); setMessage(''); setRestarting(false)
    }
  }

  if (!settings) return <div className="panel-loading"><Spinner />{t('settings.loading')}</div>
  return <form className="page-stack settings-page" onSubmit={submit}>
    <header className="page-heading"><div><p className="eyebrow">{t('settings.eyebrow')}</p><h1>{t('settings.heading')}</h1><p>{t('settings.description')}</p></div></header>
    {error && <Notice tone="danger">{error}</Notice>}
    {message && <Notice tone={restartRequired ? 'warning' : 'success'}>{message}</Notice>}

    <nav className="settings-tabs" role="tablist" aria-label={t('settings.heading')}>
      {settingsTabs.map(tab => <button key={tab.id} type="button" role="tab" id={`settings-tab-${tab.id}`} aria-controls={`settings-panel-${tab.id}`} aria-selected={settingsTab === tab.id} className={settingsTab === tab.id ? 'active' : ''} onClick={() => setSettingsTab(tab.id)}><b>{tab.index}</b><span>{t(tab.label)}</span></button>)}
    </nav>

    <div className="settings-tab-panel" role="tabpanel" id={`settings-panel-${settingsTab}`} aria-labelledby={`settings-tab-${settingsTab}`}>
    {settingsTab === 'source' && <SettingsSection index="01" title={t('settings.sourceTitle')} description={t('settings.sourceDescription')}>
      <nav className="section-tabs wide" role="tablist">
        <button type="button" role="tab" aria-selected={sourceTab === 'basic'} className={sourceTab === 'basic' ? 'active' : ''} onClick={() => setSourceTab('basic')}>{t('settings.sourceTabBasic')}</button>
        <button type="button" role="tab" aria-selected={sourceTab === 'filter'} className={sourceTab === 'filter' ? 'active' : ''} onClick={() => setSourceTab('filter')}>{t('settings.sourceTabFilter')}</button>
      </nav>
      {sourceTab === 'basic' ? <>
        <Field label={t('settings.sourceUrl')} wide><input value={settings.sourceUrl} onChange={event => update('sourceUrl', event.target.value)} /></Field>
        <Field label={t('settings.matrixSources')} hint={t('settings.matrixSourcesHint')} wide><textarea rows={5} spellCheck={false} value={(settings.matrix?.sources ?? []).join('\n')} onChange={event => update('matrix', { ...settings.matrix, sources: event.target.value.split('\n').map(value => value.trim()).filter(Boolean) })} /></Field>
        <Field label={t('settings.refreshInterval')} hint={t('settings.durationExample')}><input value={settings.refreshInterval} onChange={event => update('refreshInterval', event.target.value)} /></Field>
        <Field label={t('settings.connectTimeout')} hint={t('settings.connectTimeoutHint')}><input value={settings.connectTimeout} onChange={event => update('connectTimeout', event.target.value)} /></Field>
        <Field label={t('settings.automaticSort')}><select value={settings.selectionMode} onChange={event => update('selectionMode', event.target.value as Config['selectionMode'])}><option value="speed">{t('settings.sortSpeed')}</option><option value="ping">{t('settings.sortPing')}</option><option value="score">{t('settings.sortScore')}</option></select></Field>
        <label className="toggle-field"><input type="checkbox" checked={settings.followRankingOnRefresh} onChange={event => update('followRankingOnRefresh', event.target.checked)} /><span><strong>{t('settings.followRanking')}</strong><small>{t('settings.followRankingHint')}</small></span></label>
        <label className="toggle-field"><input type="checkbox" checked={settings.fallbackToDirect} onChange={event => update('fallbackToDirect', event.target.checked)} /><span><strong>{t('settings.fallbackToDirect')}</strong><small>{t('settings.fallbackToDirectHint')}</small></span></label>
      </> : <>
        <Field label={t('settings.filter')} hint={t('settings.filterHint')} wide><textarea rows={6} spellCheck={false} value={settings.filterExpression} onChange={event => update('filterExpression', event.target.value)} /></Field>
        <FilterHelp />
      </>}
    </SettingsSection>}

    {settingsTab === 'protocol' && <SettingsSection index="02" title={t('settings.protocolTitle')} description={t('settings.protocolDescription')}>
      <div className="protocol-order">{settings.protocolPriority.map((protocol, index) => <div key={protocol}><b>{String(index + 1).padStart(2, '0')}</b><span>{protocolLabels[protocol]}</span><button type="button" disabled={index === 0} onClick={() => moveProtocol(index, -1)}>↑</button><button type="button" disabled={index === settings.protocolPriority.length - 1} onClick={() => moveProtocol(index, 1)}>↓</button></div>)}</div>
      <div className="settings-grid nested"><Field label={t('settings.vpnUsername')}><input value={settings.vpnGateUsername} onChange={event => update('vpnGateUsername', event.target.value)} /></Field><Field label={t('settings.vpnPassword')}><input type="password" value={settings.vpnGatePassword} onChange={event => update('vpnGatePassword', event.target.value)} /></Field><Field label={t('settings.psk')}><input type="password" value={settings.vpnGatePreSharedKey} onChange={event => update('vpnGatePreSharedKey', event.target.value)} /></Field></div>
    </SettingsSection>}

    {settingsTab === 'entry' && <SettingsSection index="03" title={t('settings.socksTitle')} description={t('settings.socksDescription')}>
      <Field label={t('settings.listenAddress')}><input value={settings.socks5.listenAddress} onChange={event => update('socks5', { ...settings.socks5, listenAddress: event.target.value })} /></Field>
      <Field label={t('settings.username')} hint={t('settings.noAuthHint')}><input value={settings.socks5.username} onChange={event => update('socks5', { ...settings.socks5, username: event.target.value })} /></Field>
      <Field label={t('settings.password')}><input type="password" value={settings.socks5.password} onChange={event => update('socks5', { ...settings.socks5, password: event.target.value })} /></Field>
      <Field label={t('settings.socksIdleTimeout')} hint={t('settings.socksIdleTimeoutHint')}><input placeholder="5m" value={settings.socks5.idleTimeout} onChange={event => update('socks5', { ...settings.socks5, idleTimeout: event.target.value })} /></Field>
      <Field label={t('settings.socksHalfCloseTimeout')} hint={t('settings.socksHalfCloseTimeoutHint')}><input placeholder="1m" value={settings.socks5.halfCloseTimeout} onChange={event => update('socks5', { ...settings.socks5, halfCloseTimeout: event.target.value })} /></Field>
    </SettingsSection>}

    {settingsTab === 'probe' && <SettingsSection index="04" title={t('settings.probeTitle')} description={t('settings.probeDescription')}>
      <Field label={t('settings.dnsServers')} hint={t('settings.dnsHint')} wide><input value={settings.dnsServers.join(', ')} onChange={event => update('dnsServers', event.target.value.split(',').map(value => value.trim()))} /></Field>
      <Field label={t('settings.speedUrl')} hint={t('settings.speedUrlHint')} wide><input type="url" value={settings.speedTestUrl} onChange={event => update('speedTestUrl', event.target.value)} /></Field>
      <Field label={t('settings.speedTimeout')} hint={t('settings.speedTimeoutHint')}><input value={settings.speedTestTimeout} onChange={event => update('speedTestTimeout', event.target.value)} /></Field>
      <Field label={t('settings.healthUrl')} wide><input value={settings.monitor.url} onChange={event => update('monitor', { ...settings.monitor, url: event.target.value })} /></Field>
      <Field label={t('settings.checkInterval')}><input value={settings.monitor.interval} onChange={event => update('monitor', { ...settings.monitor, interval: event.target.value })} /></Field>
      <Field label={t('settings.requestTimeout')}><input value={settings.monitor.timeout} onChange={event => update('monitor', { ...settings.monitor, timeout: event.target.value })} /></Field>
    </SettingsSection>}

    {settingsTab === 'system' && <SettingsSection index="05" title={t('settings.adminTitle')} description={t('settings.adminDescription')}>
      <Field label={t('settings.matrixSlots')} hint={t('settings.matrixSlotsHint')}><input type="number" min={1} max={256} value={settings.matrix?.slots ?? 0} onChange={event => update('matrix', { ...settings.matrix, slots: Math.max(1, Math.min(256, Number(event.target.value) || 1)) })} /></Field>
      <Field label={t('settings.matrixPortStart')} hint={t('settings.matrixPortStartHint')}><input type="number" min={1} max={65000} value={settings.matrix?.portStart ?? 0} onChange={event => update('matrix', { ...settings.matrix, portStart: Math.max(1, Math.min(65000, Number(event.target.value) || 1)) })} /></Field>
      <Field label={t('settings.matrixPublicSocksHost')} hint={t('settings.matrixPublicSocksHostHint')}><input placeholder="proxy.example.com" value={settings.matrix?.publicSocksHost ?? ''} onChange={event => update('matrix', { ...settings.matrix, publicSocksHost: event.target.value })} /></Field>
      <Field label={t('settings.matrixShareLimit')} hint={t('settings.matrixShareLimitHint')}><input type="number" min={0} max={256} value={settings.matrix?.shareLimit ?? 0} onChange={event => update('matrix', { ...settings.matrix, shareLimit: Math.max(0, Math.min(256, Number(event.target.value) || 0)) })} /></Field>
      <Field label={t('settings.dialMinMs')} hint={t('settings.dialRangeHint')}><input type="number" min={100} max={30000} step={100} value={settings.matrix?.dialMinMs ?? 900} onChange={event => update('matrix', { ...settings.matrix, dialMinMs: Math.max(0, Math.min(30000, Number(event.target.value) || 0)) })} /></Field>
      <Field label={t('settings.dialMaxMs')} hint={t('settings.dialRangeHint')}><input type="number" min={100} max={30000} step={100} value={settings.matrix?.dialMaxMs ?? 1500} onChange={event => update('matrix', { ...settings.matrix, dialMaxMs: Math.max(0, Math.min(30000, Number(event.target.value) || 0)) })} /></Field>
      <Field label={t('settings.databasePath')}><input value={settings.databasePath} onChange={event => update('databasePath', event.target.value)} /></Field>
      <Field label={t('settings.webListen')}><input value={settings.web.listenAddress} onChange={event => update('web', { ...settings.web, listenAddress: event.target.value })} /></Field>
      <Field label={t('settings.webUsername')}><input value={settings.web.username} onChange={event => update('web', { ...settings.web, username: event.target.value })} /></Field>
      <Field label={t('settings.newPassword')} hint={t('settings.newPasswordHint')}><input type="password" minLength={8} value={newPassword} onChange={event => setNewPassword(event.target.value)} /></Field>
    </SettingsSection>}
    </div>

    <div className="settings-actions"><div><strong>{restartRequired ? t('settings.restartPending') : t('settings.writesLocal')}</strong><small>{t('settings.sensitiveHint')}</small></div><button type="button" className="danger-button" disabled={restarting} onClick={restart}>{restarting ? <Spinner /> : <RestartIcon />}{t('settings.restart')}</button><button className="primary-button" disabled={saving}>{saving ? <Spinner /> : null}{t('settings.save')}</button></div>
  </form>
}

function SettingsSection({ index, title, description, children, stacked = false }: { index: string; title: string; description: string; children: ReactNode; stacked?: boolean }) {
  return <section className={`settings-section${stacked ? ' stacked' : ''}`}><header><span>{index}</span><div><h2>{title}</h2><p>{description}</p></div></header><div className="settings-grid">{children}</div></section>
}

function FilterHelp() {
  const { t } = useTranslation()
  return <aside className="filter-help">
    <div className="filter-help-intro"><strong>{t('filter.rules')}</strong><p>{t('filter.intro')}</p></div>
    <div className="filter-help-grid">
      <section><strong>{t('filter.textFields')}</strong><code>hostName</code><code>ip</code><code>countryLong</code><code>countryShort</code><code>logType</code><code>operator</code><code>operatorMessage</code></section>
      <section><strong>{t('filter.numberFields')}</strong><code>score</code><code>pingMs</code><code>speedBps</code><code>sessions</code><code>uptimeMs</code><code>totalUsers</code><code>totalTraffic</code></section>
      <section><strong>{t('filter.protocols')}</strong><code>protocols</code><p>{t('filter.protocolsHint')}</p></section>
      <section><strong>{t('filter.helpers')}</strong><code>cidrContains(cidr, ip)</code><code>includesIgnoreCase(value, search)</code><p>{t('filter.helpersHint')}</p></section>
    </div>
    <div className="filter-units"><span><code>pingMs</code>: {t('filter.milliseconds')}</span><span><code>speedBps</code>: bit/s</span><span><code>uptimeMs</code>: {t('filter.milliseconds')}</span><span><code>totalTraffic</code>: bytes</span></div>
    <strong className="filter-example-title">{t('filter.example')}</strong>
    <pre><code>{`node.countryShort === "JP" &&
node.pingMs > 0 && node.pingMs < 100 &&
node.protocols.includes("openvpn_udp") &&
!includesIgnoreCase(node.operatorMessage, "academic use only")`}</code></pre>
    <p className="filter-help-note">{t('filter.operators')} <code>cidrContains("219.100.0.0/16", node.ip)</code></p>
  </aside>
}

function LanguageSelect() {
  const { t, i18n } = useTranslation()
  const locale = resolvedLocale(i18n.resolvedLanguage)

  return <select
    className="locale-select"
    aria-label={t('language.label')}
    value={locale}
    onChange={event => void i18n.changeLanguage(event.target.value)}
  >
    {Object.entries(localeConfig.resources).map(([value, resource]) => <option value={value} key={value}>{resource.label}</option>)}
  </select>
}

function Field({ label, hint, wide, children }: { label: string; hint?: string; wide?: boolean; children: ReactNode }) {
  return <label className={`field ${wide ? 'wide' : ''}`}><span>{label}</span>{children}{hint && <small>{hint}</small>}</label>
}

function Notice({ tone, children }: { tone: 'danger' | 'warning' | 'success'; children: ReactNode }) {
  return <div className={`notice ${tone}`}><i />{children}</div>
}

function Spinner() { return <span className="spinner" aria-hidden="true" /> }
function RefreshIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M20 11a8 8 0 1 0-2.34 5.66M20 4v7h-7" /></svg> }
function RestartIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 4v6h6M20 20v-6h-6M5.5 15a8 8 0 0 0 13-3M18.5 9A8 8 0 0 0 5.5 6" /></svg> }
function SearchIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="11" cy="11" r="7" /><path d="m20 20-4-4" /></svg> }
function SpeedIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 17a8 8 0 1 1 16 0M12 17l4-6" /><path d="M7 17h10" /></svg> }
function StopIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><rect x="7" y="7" width="10" height="10" rx="1" /></svg> }
function LinkIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M10 13a5 5 0 0 0 7.07 0l2.83-2.83a5 5 0 0 0-7.07-7.07L11 4.93" /><path d="M14 11a5 5 0 0 0-7.07 0L4.1 13.83a5 5 0 0 0 7.07 7.07L13 19.07" /></svg> }
function ThemeIcon({ theme }: { theme: Theme }) { return theme === 'dark' ? <svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="4" /><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" /></svg> : <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M20 15.5A8.5 8.5 0 0 1 8.5 4 8.5 8.5 0 1 0 20 15.5Z" /></svg> }

function delay(milliseconds: number) { return new Promise(resolve => window.setTimeout(resolve, milliseconds)) }
async function writeClipboard(value: string) {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(value)
    return
  }
  const field = document.createElement('textarea')
  field.value = value
  field.setAttribute('readonly', '')
  field.style.position = 'fixed'
  field.style.opacity = '0'
  document.body.appendChild(field)
  field.select()
  const copied = document.execCommand('copy')
  document.body.removeChild(field)
  if (!copied) throw new Error('clipboard unavailable')
}

function errorMessage(reason: unknown) { return reason instanceof Error ? reason.message : String(reason) }
function orderedProtocols(protocols: Protocol[], priority: Protocol[]) {
  const order = [...priority, ...protocolDisplayOrder.filter(protocol => !priority.includes(protocol))]
  return order.filter(protocol => protocols.includes(protocol))
}
function preferredTheme(): Theme {
  const saved = window.localStorage.getItem('proxygate:theme')
  if (saved === 'light' || saved === 'dark') return saved
  return window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark'
}
function preferredNodeView(): NodeViewMode {
  const saved = window.localStorage.getItem('proxygate:nodeView')
  return saved === 'cards' || saved === 'list' || saved === 'table' ? saved : 'cards'
}
function normalizedPing(value: number) { return value > 0 ? value : Number.MAX_SAFE_INTEGER }
function stateLabel(state: string | undefined, t: TFunction) {
  const labels: Record<string, string> = {
    connected: t('status.connected'),
    connecting: t('status.connecting'),
    error: t('status.error'),
    idle: t('status.idle'),
  }
  return labels[state || ''] || state || t('status.loading')
}
function formatDateTime(value: string, locale: string) { return new Date(value).toLocaleString(locale, { hour12: false }) }
function formatRelativeTime(value: string | Date | undefined, t: TFunction) {
  if (!value) return t('time.never')
  const milliseconds = Date.now() - new Date(value).getTime()
  if (milliseconds < 10_000) return t('time.now')
  if (milliseconds < 60_000) return t('time.secondsAgo', { count: Math.floor(milliseconds / 1_000) })
  if (milliseconds < 3_600_000) return t('time.minutesAgo', { count: Math.floor(milliseconds / 60_000) })
  return t('time.hoursAgo', { count: Math.floor(milliseconds / 3_600_000) })
}
function formatNumber(value: number, locale: string) { return new Intl.NumberFormat(locale, { notation: value >= 10_000 ? 'compact' : 'standard' }).format(value) }
function formatRate(bitsPerSecond: number) {
  if (!bitsPerSecond) return '—'
  const units = ['bps', 'Kbps', 'Mbps', 'Gbps']
  let value = bitsPerSecond; let unit = 0
  while (value >= 1_000 && unit < units.length - 1) { value /= 1_000; unit++ }
  return `${value.toFixed(value >= 10 ? 1 : 2)} ${units[unit]}`
}
function countryFlag(code: string) {
  const normalized = code.toUpperCase()
  if (!/^[A-Z]{2}$/.test(normalized)) return '◎'
  return String.fromCodePoint(...[...normalized].map(character => 127397 + character.charCodeAt(0)))
}

export default App

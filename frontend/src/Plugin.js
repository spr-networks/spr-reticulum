import React, { useEffect, useState } from 'react'
import {
  api,
  useAlert,
  timeAgo,
  Page,
  ListHeader,
  Card,
  SectionHeader,
  StatTile,
  StatusDot,
  Toggle,
  TextField,
  ModalForm,
  ModalConfirm,
  Loading,
  EmptyState,
  Button,
  ButtonText,
  Text,
  HStack,
  VStack,
  Pressable,
  Icon,
  CopyIcon,
  GlobeIcon,
  ShareIcon
} from '@spr-networks/plugin-ui'

const PLUGIN_BASE = `/plugins/${api.pluginURI() || 'spr-reticulum'}`

// Community entrypoints listed on directory.rns.recipes (the original
// RNS testnet entrypoint has been decommissioned upstream).
const PRESETS = [
  { Name: 'Beleth RNS Hub', TargetHost: 'rns.beleth.net', TargetPort: 4242 },
  { Name: 'RMAP', TargetHost: 'rmap.world', TargetPort: 4242 }
]

// Mirrors the backend allow-lists (config.go) so errors show before a round trip.
const NAME_RE = /^[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$/
const HOST_RE = /^[A-Za-z0-9:][A-Za-z0-9.:_-]{0,252}$/
const RESERVED_NAMES = ['AutoInterface', 'TCPServer']

const MONO = { '@base': { fontFamily: 'monospace' } }

const humanBytes = (n) => {
  if (n === null || n === undefined) return '—'
  let v = Number(n)
  for (const unit of ['B', 'KB', 'MB', 'GB', 'TB']) {
    if (v < 1024 || unit === 'TB') {
      return `${v.toFixed(v >= 100 || unit === 'B' ? 0 : 1)} ${unit}`
    }
    v /= 1024
  }
}

const humanUptime = (secs) => {
  const s = Math.floor(Number(secs) || 0)
  if (s < 1) return null
  if (s < 60) return `${s}s`
  if (s < 3600) return `${Math.floor(s / 60)}m`
  if (s < 86400) return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`
  return `${Math.floor(s / 86400)}d ${Math.floor((s % 86400) / 3600)}h`
}

// One row of the interfaces list: status dot, bold name, muted type + mono
// target, per-interface RX/TX in mono, quiet actions on the right.
const InterfaceRow = ({
  name,
  typeLabel,
  target,
  enabled,
  stat,
  toggle,
  onRemove,
  disabled
}) => (
  <HStack
    justifyContent="space-between"
    alignItems="center"
    minHeight={52}
    py="$1"
  >
    <HStack space="sm" alignItems="center" flexShrink={1}>
      <StatusDot online={enabled && !!stat?.Online} warn={enabled && !stat?.Online} />
      <VStack flexShrink={1}>
        <Text size="sm" bold>
          {name}
        </Text>
        <HStack space="xs" alignItems="center" flexWrap="wrap">
          <Text size="xs" color="$muted500">
            {typeLabel}
          </Text>
          {target ? (
            <Text size="xs" color="$muted500" sx={MONO}>
              · {target}
            </Text>
          ) : null}
          {stat?.Clients != null ? (
            <Text size="xs" color="$muted500">
              · {stat.Clients} client{stat.Clients === 1 ? '' : 's'}
            </Text>
          ) : null}
        </HStack>
      </VStack>
    </HStack>
    <HStack space="md" alignItems="center" flexShrink={0}>
      <Text size="xs" color="$muted500" textAlign="right" sx={MONO}>
        {stat ? `↓${humanBytes(stat.RXBytes)} ↑${humanBytes(stat.TXBytes)}` : '—'}
      </Text>
      <Toggle value={enabled} disabled={disabled} onPress={toggle} label={`Enable ${name}`} />
      {onRemove ? (
        <Button
          size="xs"
          variant="outline"
          action="secondary"
          isDisabled={disabled}
          onPress={onRemove}
        >
          <ButtonText>Remove</ButtonText>
        </Button>
      ) : null}
    </HStack>
  </HStack>
)

export default function Plugin() {
  const alert = useAlert()
  const [status, setStatus] = useState(null)
  const [config, setConfig] = useState(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [showRestart, setShowRestart] = useState(false)
  const [restarting, setRestarting] = useState(false)

  // add-interface modal
  const [showAdd, setShowAdd] = useState(false)
  const [fName, setFName] = useState('')
  const [fHost, setFHost] = useState('')
  const [fPort, setFPort] = useState('4242')
  const [fErrors, setFErrors] = useState({})

  // remove confirmation target ({Name, TargetHost, TargetPort} or null)
  const [removeTarget, setRemoveTarget] = useState(null)

  // TCP server port editor
  const [serverPort, setServerPort] = useState('')

  // path table
  const [paths, setPaths] = useState(null)
  const [pathsLoading, setPathsLoading] = useState(false)

  const refreshStatus = () => {
    api
      .get(`${PLUGIN_BASE}/status`)
      .then(setStatus)
      .catch(() => {}) // background poll: keep last known state quietly
  }

  const refresh = () => {
    setLoading(true)
    Promise.all([
      api.get(`${PLUGIN_BASE}/status`),
      api.get(`${PLUGIN_BASE}/config`)
    ])
      .then(([s, c]) => {
        setStatus(s)
        setConfig(c)
      })
      .catch((err) => alert.error('Failed to load plugin data', err))
      .finally(() => setLoading(false))
  }

  useEffect(() => {
    refresh()
    const t = setInterval(refreshStatus, 15000)
    return () => clearInterval(t)
  }, [])

  useEffect(() => {
    if (config) {
      setServerPort(String(config.TCPServerInterface?.ListenPort ?? 4242))
    }
  }, [config])

  // Persist a config; the UI reflects the change only after the backend
  // accepts it. Every save restarts rnsd (the backend re-renders the RNS
  // config), hence the toast wording.
  const save = (next, msg = 'Saved — applying restarts rnsd') => {
    setSaving(true)
    return api
      .put(`${PLUGIN_BASE}/config`, next)
      .then((saved) => {
        setConfig(saved)
        alert.success(msg)
        setTimeout(refreshStatus, 4000)
        return saved
      })
      .catch((err) => {
        alert.error('Failed to save', err)
        throw err
      })
      .finally(() => setSaving(false))
  }

  const restart = () => {
    setRestarting(true)
    api
      .post(`${PLUGIN_BASE}/restart`)
      .then(() => {
        alert.success('rnsd restarted')
        setTimeout(refreshStatus, 4000)
      })
      .catch((err) => alert.error('Failed to restart', err))
      .finally(() => setRestarting(false))
  }

  const loadPaths = () => {
    setPathsLoading(true)
    api
      .get(`${PLUGIN_BASE}/path-table`)
      .then(setPaths)
      .catch((err) => alert.error('Failed to load path table', err))
      .finally(() => setPathsLoading(false))
  }

  const copyText = (value) => {
    if (navigator.clipboard?.writeText) {
      navigator.clipboard.writeText(value).then(
        () => alert.success('Copied'),
        () => alert.error('Copy failed')
      )
    }
  }

  const resetAddForm = () => {
    setFName('')
    setFHost('')
    setFPort('4242')
    setFErrors({})
  }

  const applyPreset = (preset) => {
    setFName(preset.Name)
    setFHost(preset.TargetHost)
    setFPort(String(preset.TargetPort))
    setFErrors({})
  }

  const validateAdd = (name, host, port) => {
    const errors = {}
    if (!name) errors.name = 'Name is required'
    else if (!NAME_RE.test(name))
      errors.name = 'Use letters, digits, spaces, ". _ -" (max 64 characters)'
    else if (RESERVED_NAMES.includes(name))
      errors.name = `"${name}" is reserved for the built-in interface`
    else if (config.TCPClientInterfaces.some((i) => i.Name === name))
      errors.name = 'An interface with this name already exists'
    if (!host) errors.host = 'Host is required'
    else if (!HOST_RE.test(host)) errors.host = 'Enter a hostname or IP address'
    const p = parseInt(port, 10)
    if (!(p >= 1 && p <= 65535)) errors.port = 'Port must be between 1 and 65535'
    return errors
  }

  const submitAdd = () => {
    const name = fName.trim()
    const host = fHost.trim()
    const errors = validateAdd(name, host, fPort)
    setFErrors(errors)
    if (Object.keys(errors).length > 0) return
    const next = {
      ...config,
      TCPClientInterfaces: [
        ...config.TCPClientInterfaces,
        { Name: name, TargetHost: host, TargetPort: parseInt(fPort, 10), Enabled: true }
      ]
    }
    save(next, `Connecting to ${name} — rnsd is restarting`)
      .then(() => {
        setShowAdd(false)
        resetAddForm()
      })
      .catch(() => {})
  }

  const removeInterface = (name) => {
    const next = {
      ...config,
      TCPClientInterfaces: config.TCPClientInterfaces.filter((i) => i.Name !== name)
    }
    save(next, `Removed ${name} — rnsd is restarting`).catch(() => {})
  }

  const toggleClient = (name) => {
    const next = {
      ...config,
      TCPClientInterfaces: config.TCPClientInterfaces.map((i) =>
        i.Name === name ? { ...i, Enabled: !i.Enabled } : i
      )
    }
    save(next).catch(() => {})
  }

  if (loading) {
    return (
      <Page>
        <Loading text="Loading Reticulum status..." />
      </Page>
    )
  }

  if (!config) {
    return (
      <Page>
        <Card>
          <EmptyState
            title="Backend unreachable"
            description="Could not reach the spr-reticulum backend. The container may still be starting."
          >
            <Button size="sm" onPress={refresh}>
              <ButtonText>Retry</ButtonText>
            </Button>
          </EmptyState>
        </Card>
      </Page>
    )
  }

  const running = !!status?.Running
  const statFor = (shortName) =>
    status?.Interfaces?.find((s) => s.ShortName === shortName)

  // "Interfaces up" counts the interfaces configured in this plugin
  // (AutoInterface, TCP clients, TCP server), not rnsd internals.
  const configured = [
    ...(config.AutoInterfaceEnabled ? ['AutoInterface'] : []),
    ...config.TCPClientInterfaces.filter((i) => i.Enabled).map((i) => i.Name),
    ...(config.TCPServerInterface?.Enabled ? ['TCPServer'] : [])
  ]
  const upCount = configured.filter((n) => statFor(n)?.Online).length

  const uptime = humanUptime(status?.UptimeSeconds)
  const serverEnabled = !!config.TCPServerInterface?.Enabled
  const serverPortNum = parseInt(serverPort, 10)
  const serverPortValid = serverPortNum >= 1 && serverPortNum <= 65535
  const serverPortDirty =
    serverPort !== String(config.TCPServerInterface?.ListenPort ?? 4242)

  return (
    <Page>
      <ListHeader
        title="Reticulum"
        description="Always-on Reticulum network node (rnsd) on your router"
        mark="rn"
        status={running ? 'Running' : 'Stopped'}
        statusAction={running ? 'success' : 'muted'}
      >
        <Button
          size="sm"
          variant="outline"
          action="secondary"
          isDisabled={restarting}
          onPress={() => setShowRestart(true)}
        >
          <ButtonText>{restarting ? 'Restarting…' : 'Restart'}</ButtonText>
        </Button>
      </ListHeader>

      <Card>
        <SectionHeader
          title="Node"
          right={
            <HStack space="sm" alignItems="center">
              <StatusDot online={running} />
              <Text size="sm" fontWeight="$medium">
                {running ? 'Running' : 'Stopped'}
              </Text>
              {running && uptime ? (
                <Text size="xs" color="$muted500">
                  up {uptime}
                </Text>
              ) : null}
            </HStack>
          }
        />
        <VStack space="md">
          <HStack justifyContent="space-between" alignItems="center">
            <VStack flexShrink={1} pr="$4">
              <Text size="sm" fontWeight="$semibold">
                Transport
              </Text>
              <Text size="xs" color="$muted500">
                Routes traffic for other RNS peers. Applying restarts rnsd.
              </Text>
            </VStack>
            <Toggle
              value={!!config.EnableTransport}
              disabled={saving}
              label="Transport"
              onPress={() =>
                save({ ...config, EnableTransport: !config.EnableTransport }).catch(
                  () => {}
                )
              }
            />
          </HStack>

          <HStack flexWrap="wrap" gap="$2">
            <StatTile label="RNS Version" value={status?.Version || '—'} mono />
            <StatTile
              label="Interfaces Up"
              value={`${upCount}/${configured.length}`}
            />
            <StatTile label="RX" value={humanBytes(status?.RXBytes)} mono />
            <StatTile label="TX" value={humanBytes(status?.TXBytes)} mono />
          </HStack>

          {status?.TransportID ? (
            <HStack space="md" alignItems="center" flexWrap="wrap">
              <Text size="sm" color="$muted500" minWidth={132}>
                Transport identity
              </Text>
              <Text size="sm" flexShrink={1} sx={MONO}>
                {status.TransportID}
              </Text>
              <Pressable
                onPress={() => copyText(status.TransportID)}
                aria-label="Copy transport identity"
              >
                <Icon as={CopyIcon} size="sm" color="$muted400" />
              </Pressable>
            </HStack>
          ) : null}

          {status?.Error ? (
            <Text size="sm" color="$muted500">
              {status.Error}
            </Text>
          ) : null}
        </VStack>
      </Card>

      <Card>
        <SectionHeader
          title="Interfaces"
          count={1 + config.TCPClientInterfaces.length}
          right={
            <Button size="sm" isDisabled={saving} onPress={() => setShowAdd(true)}>
              <ButtonText>Add interface</ButtonText>
            </Button>
          }
        />
        <VStack space="xs">
          <InterfaceRow
            name="AutoInterface"
            typeLabel="LAN auto-discovery (built-in)"
            enabled={!!config.AutoInterfaceEnabled}
            stat={statFor('AutoInterface')}
            disabled={saving}
            toggle={() =>
              save({
                ...config,
                AutoInterfaceEnabled: !config.AutoInterfaceEnabled
              }).catch(() => {})
            }
          />

          {config.TCPClientInterfaces.map((iface) => (
            <InterfaceRow
              key={iface.Name}
              name={iface.Name}
              typeLabel="TCP client"
              target={`${iface.TargetHost}:${iface.TargetPort}`}
              enabled={!!iface.Enabled}
              stat={statFor(iface.Name)}
              disabled={saving}
              toggle={() => toggleClient(iface.Name)}
              onRemove={() => setRemoveTarget(iface)}
            />
          ))}

          {config.TCPClientInterfaces.length === 0 ? (
            <EmptyState
              icon={GlobeIcon}
              title="No outbound connections"
              description="AutoInterface only reaches RNS peers on your LAN. Connect to a community hub over TCP to join the wider Reticulum network."
            >
              <Button size="sm" onPress={() => setShowAdd(true)}>
                <ButtonText>Add interface</ButtonText>
              </Button>
            </EmptyState>
          ) : null}
        </VStack>
      </Card>

      <Card>
        <SectionHeader title="Settings" />
        <VStack space="md">
          <HStack justifyContent="space-between" alignItems="center">
            <VStack flexShrink={1} pr="$4">
              <Text size="sm" fontWeight="$semibold">
                TCP server
              </Text>
              <Text size="xs" color="$muted500">
                Accept inbound RNS connections on the container IP (plugin
                bridge only, never exposed on the WAN)
              </Text>
            </VStack>
            <Toggle
              value={serverEnabled}
              disabled={saving}
              label="TCP server"
              onPress={() =>
                save({
                  ...config,
                  TCPServerInterface: {
                    ...config.TCPServerInterface,
                    Enabled: !serverEnabled
                  }
                }).catch(() => {})
              }
            />
          </HStack>

          {serverEnabled ? (
            <VStack space="sm">
              <TextField
                label="Listen port"
                value={serverPort}
                onChangeText={setServerPort}
                placeholder="4242"
                helper="Bound to the container IP on the spr-reticulum bridge. Applying restarts rnsd."
                error={
                  serverPortDirty && !serverPortValid
                    ? 'Port must be between 1 and 65535'
                    : undefined
                }
              />
              <HStack>
                <Button
                  size="sm"
                  isDisabled={saving || !serverPortDirty || !serverPortValid}
                  onPress={() =>
                    save({
                      ...config,
                      TCPServerInterface: {
                        ...config.TCPServerInterface,
                        ListenPort: serverPortNum
                      }
                    }).catch(() => {})
                  }
                >
                  <ButtonText>{saving ? 'Saving…' : 'Save port'}</ButtonText>
                </Button>
              </HStack>
            </VStack>
          ) : null}
        </VStack>
      </Card>

      <Card>
        <SectionHeader
          title="Paths"
          count={paths ? paths.length : undefined}
          right={
            <Button
              size="xs"
              variant="outline"
              action="secondary"
              isDisabled={pathsLoading || !running}
              onPress={loadPaths}
            >
              <ButtonText>
                {pathsLoading ? 'Loading…' : paths ? 'Reload' : 'Load'}
              </ButtonText>
            </Button>
          }
        />
        {paths === null ? (
          <Text size="sm" color="$muted500">
            {running
              ? 'Destinations this node knows a route to. Load the table to see what rnsd has learned.'
              : 'Start rnsd to query the path table.'}
          </Text>
        ) : paths.length === 0 ? (
          <EmptyState
            icon={ShareIcon}
            title="No paths yet"
            description="Paths appear as the network is used — announces and traffic passing through your interfaces teach this node routes to destinations."
          />
        ) : (
          <VStack space="sm">
            {paths.slice(0, 50).map((p) => (
              <HStack
                key={`${p.hash}-${p.interface}`}
                justifyContent="space-between"
                alignItems="center"
              >
                <VStack flexShrink={1}>
                  <Text size="xs" sx={MONO}>
                    {p.hash}
                  </Text>
                  <Text size="xs" color="$muted500">
                    {p.interface}
                  </Text>
                </VStack>
                <VStack alignItems="flex-end" flexShrink={0}>
                  <Text size="xs">
                    {p.hops} hop{p.hops === 1 ? '' : 's'}
                  </Text>
                  <Text size="xs" color="$muted500">
                    {(p.timestamp &&
                      timeAgo(new Date(p.timestamp * 1000).toISOString())) ||
                      '—'}
                  </Text>
                </VStack>
              </HStack>
            ))}
            {paths.length > 50 ? (
              <Text size="xs" color="$muted500">
                Showing 50 of {paths.length} paths.
              </Text>
            ) : null}
          </VStack>
        )}
      </Card>

      <ModalForm
        isOpen={showAdd}
        onClose={() => {
          setShowAdd(false)
          resetAddForm()
        }}
        title="Add TCP interface"
      >
        <VStack space="md" pb="$2">
          <VStack space="xs">
            <Text size="xs" color="$muted500">
              Community hubs (from directory.rns.recipes) — tap to prefill:
            </Text>
            <HStack space="sm" flexWrap="wrap">
              {PRESETS.map((p) => (
                <Button
                  key={p.Name}
                  size="xs"
                  variant="outline"
                  action="secondary"
                  isDisabled={config.TCPClientInterfaces.some(
                    (i) => i.Name === p.Name
                  )}
                  onPress={() => applyPreset(p)}
                >
                  <ButtonText>{p.Name}</ButtonText>
                </Button>
              ))}
            </HStack>
          </VStack>

          <TextField
            label="Name"
            value={fName}
            onChangeText={(v) => {
              setFName(v)
              if (fErrors.name) setFErrors({ ...fErrors, name: undefined })
            }}
            placeholder="My RNS Hub"
            helper="Shown in the interface list"
            error={fErrors.name}
          />
          <TextField
            label="Host"
            value={fHost}
            onChangeText={(v) => {
              setFHost(v)
              if (fErrors.host) setFErrors({ ...fErrors, host: undefined })
            }}
            placeholder="rns.example.net"
            helper="Hostname or IP of the remote RNS node"
            error={fErrors.host}
          />
          <TextField
            label="Port"
            value={fPort}
            onChangeText={(v) => {
              setFPort(v)
              if (fErrors.port) setFErrors({ ...fErrors, port: undefined })
            }}
            placeholder="4242"
            error={fErrors.port}
          />
          <HStack space="sm">
            <Button size="sm" isDisabled={saving} onPress={submitAdd}>
              <ButtonText>{saving ? 'Adding…' : 'Add interface'}</ButtonText>
            </Button>
            <Button
              size="sm"
              variant="outline"
              action="secondary"
              onPress={() => {
                setShowAdd(false)
                resetAddForm()
              }}
            >
              <ButtonText>Cancel</ButtonText>
            </Button>
          </HStack>
          <Text size="xs" color="$muted500">
            Adding an interface restarts rnsd; existing links re-establish
            automatically.
          </Text>
        </VStack>
      </ModalForm>

      <ModalConfirm
        isOpen={removeTarget !== null}
        onClose={() => setRemoveTarget(null)}
        onConfirm={() => {
          if (removeTarget) removeInterface(removeTarget.Name)
          setRemoveTarget(null)
        }}
        title={`Remove ${removeTarget?.Name}?`}
        message={`The connection to ${removeTarget?.TargetHost}:${removeTarget?.TargetPort} closes and rnsd restarts. Paths learned through it are dropped.`}
        confirmText="Remove"
        destructive
      />

      <ModalConfirm
        isOpen={showRestart}
        onClose={() => setShowRestart(false)}
        onConfirm={() => {
          setShowRestart(false)
          restart()
        }}
        title="Restart rnsd?"
        message="The Reticulum daemon restarts and links re-establish automatically. Traffic pauses for a few seconds."
        confirmText="Restart"
      />
    </Page>
  )
}

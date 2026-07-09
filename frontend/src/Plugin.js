import React, { useEffect, useState } from 'react'
import {
  api,
  useAlert,
  Page,
  ListHeader,
  Card,
  SectionHeader,
  StatTile,
  KeyVal,
  StatusDot,
  Toggle,
  TextField,
  ModalConfirm,
  Loading,
  EmptyState,
  Button,
  ButtonText,
  Text,
  HStack,
  VStack
} from '@spr-networks/plugin-ui'

const PLUGIN_BASE = `/plugins/${api.pluginURI() || 'spr-reticulum'}`

// Community entrypoints listed on directory.rns.recipes (the original
// RNS testnet entrypoint has been decommissioned upstream).
const PRESETS = [
  { Name: 'Beleth RNS Hub', TargetHost: 'rns.beleth.net', TargetPort: 4242 },
  { Name: 'RMAP', TargetHost: 'rmap.world', TargetPort: 4242 }
]

const humanBytes = (n) => {
  if (n === null || n === undefined) return '-'
  let v = Number(n)
  for (const unit of ['B', 'KB', 'MB', 'GB', 'TB']) {
    if (v < 1024 || unit === 'TB') {
      return `${v.toFixed(v >= 100 || unit === 'B' ? 0 : 1)} ${unit}`
    }
    v /= 1024
  }
}

export default function Plugin() {
  const alert = useAlert()
  const [status, setStatus] = useState(null)
  const [config, setConfig] = useState(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [showRestart, setShowRestart] = useState(false)
  const [paths, setPaths] = useState(null)
  const [newName, setNewName] = useState('')
  const [newHost, setNewHost] = useState('')
  const [newPort, setNewPort] = useState('4242')

  const refreshStatus = () => {
    api
      .get(`${PLUGIN_BASE}/status`)
      .then(setStatus)
      .catch((err) => alert.error('Failed to load status', err))
  }

  const refresh = () => {
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

  const save = (cfg) => {
    setSaving(true)
    api
      .put(`${PLUGIN_BASE}/config`, cfg)
      .then((saved) => {
        setConfig(saved)
        alert.success('Saved - restarting rnsd')
        setTimeout(refreshStatus, 4000)
      })
      .catch((err) => alert.error('Failed to save config', err))
      .finally(() => setSaving(false))
  }

  const restart = () => {
    api
      .post(`${PLUGIN_BASE}/restart`)
      .then(() => {
        alert.success('rnsd restarted')
        setTimeout(refreshStatus, 4000)
      })
      .catch((err) => alert.error('Failed to restart', err))
  }

  const loadPaths = () => {
    api
      .get(`${PLUGIN_BASE}/path-table`)
      .then(setPaths)
      .catch((err) => alert.error('Failed to load path table', err))
  }

  const addInterface = (preset) => {
    const iface = preset || {
      Name: newName.trim(),
      TargetHost: newHost.trim(),
      TargetPort: parseInt(newPort, 10)
    }
    if (!iface.Name || !iface.TargetHost || !iface.TargetPort) {
      alert.error('Name, host and port are required')
      return
    }
    if (config.TCPClientInterfaces.some((i) => i.Name === iface.Name)) {
      alert.error(`Interface "${iface.Name}" already exists`)
      return
    }
    const next = {
      ...config,
      TCPClientInterfaces: [
        ...config.TCPClientInterfaces,
        { ...iface, Enabled: true }
      ]
    }
    setConfig(next)
    if (!preset) {
      setNewName('')
      setNewHost('')
      setNewPort('4242')
    }
    save(next)
  }

  const removeInterface = (name) => {
    const next = {
      ...config,
      TCPClientInterfaces: config.TCPClientInterfaces.filter(
        (i) => i.Name !== name
      )
    }
    setConfig(next)
    save(next)
  }

  const toggleInterface = (name) => {
    const next = {
      ...config,
      TCPClientInterfaces: config.TCPClientInterfaces.map((i) =>
        i.Name === name ? { ...i, Enabled: !i.Enabled } : i
      )
    }
    setConfig(next)
    save(next)
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
        <EmptyState
          title="Not available"
          description="Could not reach the spr-reticulum backend."
        >
          <Button size="sm" onPress={refresh}>
            <ButtonText>Retry</ButtonText>
          </Button>
        </EmptyState>
      </Page>
    )
  }

  const ifaces = status?.Interfaces || []
  const online = ifaces.filter((i) => i.Online).length

  return (
    <Page>
      <ListHeader
        title="Reticulum"
        description="Reticulum network stack node (rnsd)"
        mark="rn"
        status={status?.Running ? 'Running' : 'Stopped'}
        statusAction={status?.Running ? 'success' : 'muted'}
      >
        <Button
          size="sm"
          variant="outline"
          onPress={() => setShowRestart(true)}
        >
          <ButtonText>Restart</ButtonText>
        </Button>
      </ListHeader>

      <Card>
        <SectionHeader
          title="Node"
          right={<StatusDot online={!!status?.Running} />}
        />
        <HStack flexWrap="wrap" gap="$2">
          <StatTile
            label="State"
            value={status?.Running ? 'Running' : 'Stopped'}
          />
          <StatTile label="RNS Version" value={status?.Version || '-'} mono />
          <StatTile
            label="Transport"
            value={status?.TransportEnabled ? 'On' : 'Off'}
          />
          <StatTile label="Interfaces Up" value={`${online}/${ifaces.length}`} />
          <StatTile label="RX" value={humanBytes(status?.RXBytes)} mono />
          <StatTile label="TX" value={humanBytes(status?.TXBytes)} mono />
        </HStack>
        {status?.TransportID ? (
          <KeyVal label="Transport Identity" value={status.TransportID} mono />
        ) : null}
        {status?.Error ? (
          <Text size="sm" color="$muted500">
            {status.Error}
          </Text>
        ) : null}
      </Card>

      <Card>
        <SectionHeader title="Interfaces" count={ifaces.length} />
        {ifaces.length === 0 ? (
          <Text size="sm" color="$muted500">
            No interface status yet - rnsd may still be starting.
          </Text>
        ) : (
          <VStack space="sm">
            {ifaces.map((iface) => (
              <HStack
                key={iface.Name}
                justifyContent="space-between"
                alignItems="center"
              >
                <HStack space="sm" alignItems="center" flexShrink={1}>
                  <StatusDot online={iface.Online} />
                  <VStack flexShrink={1}>
                    <Text size="sm" bold>
                      {iface.ShortName || iface.Name}
                    </Text>
                    <Text size="xs" color="$muted500">
                      {iface.Type}
                      {iface.Clients !== undefined && iface.Clients !== null
                        ? ` · ${iface.Clients} client${
                            iface.Clients === 1 ? '' : 's'
                          }`
                        : ''}
                    </Text>
                  </VStack>
                </HStack>
                <Text size="xs" color="$muted500" sx={{ '@base': { fontFamily: 'monospace' } }}>
                  {'↓'}
                  {humanBytes(iface.RXBytes)} {'↑'}
                  {humanBytes(iface.TXBytes)}
                </Text>
              </HStack>
            ))}
          </VStack>
        )}
      </Card>

      <Card>
        <SectionHeader title="Settings" />
        <VStack space="md">
          <HStack justifyContent="space-between" alignItems="center">
            <VStack flexShrink={1}>
              <Text size="sm">Enable Transport</Text>
              <Text size="xs" color="$muted500">
                Route traffic, pass announces and serve path requests for
                other Reticulum peers
              </Text>
            </VStack>
            <Toggle
              value={!!config.EnableTransport}
              disabled={saving}
              onPress={() =>
                save({ ...config, EnableTransport: !config.EnableTransport })
              }
            />
          </HStack>

          <HStack justifyContent="space-between" alignItems="center">
            <VStack flexShrink={1}>
              <Text size="sm">AutoInterface</Text>
              <Text size="xs" color="$muted500">
                Peer with other RNS nodes on the spr-reticulum bridge
                (link-local IPv6)
              </Text>
            </VStack>
            <Toggle
              value={!!config.AutoInterfaceEnabled}
              disabled={saving}
              onPress={() =>
                save({
                  ...config,
                  AutoInterfaceEnabled: !config.AutoInterfaceEnabled
                })
              }
            />
          </HStack>

          <HStack justifyContent="space-between" alignItems="center">
            <VStack flexShrink={1}>
              <Text size="sm">TCP Server</Text>
              <Text size="xs" color="$muted500">
                Accept inbound RNS connections on the container IP (plugin
                bridge only, never exposed on the WAN)
              </Text>
            </VStack>
            <Toggle
              value={!!config.TCPServerInterface?.Enabled}
              disabled={saving}
              onPress={() =>
                save({
                  ...config,
                  TCPServerInterface: {
                    ...config.TCPServerInterface,
                    Enabled: !config.TCPServerInterface?.Enabled
                  }
                })
              }
            />
          </HStack>

          {config.TCPServerInterface?.Enabled ? (
            <TextField
              label="TCP Server Port"
              value={String(config.TCPServerInterface?.ListenPort || 4242)}
              onChangeText={(v) =>
                setConfig({
                  ...config,
                  TCPServerInterface: {
                    ...config.TCPServerInterface,
                    ListenPort: parseInt(v, 10) || 0
                  }
                })
              }
              onBlur={() => save(config)}
              placeholder="4242"
              helper="Bound to the container IP on the spr-reticulum bridge"
            />
          ) : null}
        </VStack>
      </Card>

      <Card>
        <SectionHeader
          title="TCP Client Interfaces"
          count={config.TCPClientInterfaces.length}
        />
        <VStack space="md">
          {config.TCPClientInterfaces.length === 0 ? (
            <Text size="sm" color="$muted500">
              No outbound interfaces configured. Add a community entrypoint
              below to join the wider network.
            </Text>
          ) : (
            config.TCPClientInterfaces.map((iface) => (
              <HStack
                key={iface.Name}
                justifyContent="space-between"
                alignItems="center"
              >
                <VStack flexShrink={1}>
                  <Text size="sm" bold>
                    {iface.Name}
                  </Text>
                  <Text size="xs" color="$muted500" sx={{ '@base': { fontFamily: 'monospace' } }}>
                    {iface.TargetHost}:{iface.TargetPort}
                  </Text>
                </VStack>
                <HStack space="sm" alignItems="center">
                  <Toggle
                    value={!!iface.Enabled}
                    disabled={saving}
                    onPress={() => toggleInterface(iface.Name)}
                  />
                  <Button
                    size="xs"
                    variant="outline"
                    action="negative"
                    isDisabled={saving}
                    onPress={() => removeInterface(iface.Name)}
                  >
                    <ButtonText>Remove</ButtonText>
                  </Button>
                </HStack>
              </HStack>
            ))
          )}

          <VStack space="sm">
            <TextField
              label="Name"
              value={newName}
              onChangeText={setNewName}
              placeholder="My RNS Hub"
            />
            <TextField
              label="Host"
              value={newHost}
              onChangeText={setNewHost}
              placeholder="rns.example.net"
            />
            <TextField
              label="Port"
              value={newPort}
              onChangeText={setNewPort}
              placeholder="4242"
            />
            <HStack space="sm" flexWrap="wrap">
              <Button size="sm" isDisabled={saving} onPress={() => addInterface()}>
                <ButtonText>Add Interface</ButtonText>
              </Button>
              {PRESETS.map((p) => (
                <Button
                  key={p.Name}
                  size="sm"
                  variant="outline"
                  isDisabled={
                    saving ||
                    config.TCPClientInterfaces.some((i) => i.Name === p.Name)
                  }
                  onPress={() => addInterface(p)}
                >
                  <ButtonText>+ {p.Name}</ButtonText>
                </Button>
              ))}
            </HStack>
            <Text size="xs" color="$muted500">
              Presets are community entrypoints from directory.rns.recipes.
              The original RNS testnet has been decommissioned upstream.
            </Text>
          </VStack>
        </VStack>
      </Card>

      <Card>
        <SectionHeader
          title="Path Table"
          count={paths ? paths.length : undefined}
          right={
            <Button size="xs" variant="outline" onPress={loadPaths}>
              <ButtonText>{paths ? 'Reload' : 'Load'}</ButtonText>
            </Button>
          }
        />
        {paths === null ? (
          <Text size="sm" color="$muted500">
            Known destination paths from the running node (rnpath).
          </Text>
        ) : paths.length === 0 ? (
          <Text size="sm" color="$muted500">
            No paths known yet.
          </Text>
        ) : (
          <VStack space="sm">
            {paths.slice(0, 50).map((p) => (
              <HStack
                key={`${p.hash}-${p.interface}`}
                justifyContent="space-between"
                alignItems="center"
              >
                <VStack flexShrink={1}>
                  <Text size="xs" sx={{ '@base': { fontFamily: 'monospace' } }}>
                    {p.hash}
                  </Text>
                  <Text size="xs" color="$muted500">
                    {p.interface}
                  </Text>
                </VStack>
                <Text size="xs" color="$muted500">
                  {p.hops} hop{p.hops === 1 ? '' : 's'}
                </Text>
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

      <ModalConfirm
        isOpen={showRestart}
        onClose={() => setShowRestart(false)}
        onConfirm={() => {
          setShowRestart(false)
          restart()
        }}
        title="Restart rnsd?"
        message="The Reticulum daemon will be restarted. Links re-establish automatically."
        confirmText="Restart"
      />
    </Page>
  )
}

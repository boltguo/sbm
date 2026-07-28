<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import QRCode from 'qrcode'
import { SwitchRoot, SwitchThumb } from 'reka-ui'
import { api, del, guard, post, put } from '../api'
import type { Inbound } from '../types'
import Icon from '../components/Icon.vue'
import Modal from '../components/Modal.vue'
import SelectControl from '../components/SelectControl.vue'
import ConfirmAction from '../components/ConfirmAction.vue'
import PasswordInput from '../components/PasswordInput.vue'
import { t } from '../i18n'

type LinkNode = Pick<Inbound, 'name' | 'link'>

const emit = defineEmits<{ toast: [message: string] }>()
const items = ref<Inbound[]>([])
const editing = ref<Inbound | null>(null)
const creating = ref(false)
const qr = ref('')
const qrItem = ref<LinkNode | null>(null)
const createForm = ref({ type: 'vless-reality' as Inbound['type'], name: t('protocol.defaultName'), port: 8443 })
const duplicate = computed(() => items.value.some(i => i.type === createForm.value.type))
const protocolOptions = [{ value: 'vless-reality', label: 'VLESS + Vision + REALITY' }, { value: 'hysteria2', label: 'Hysteria2' }]
const obfsOptions = computed(() => [{ value: 'none', label: t('protocol.off') }, { value: 'salamander', label: 'Salamander' }])

const safe = guard(message => emit('toast', message))
const load = safe(async () => { items.value = await api<Inbound[]>('/api/inbounds') })
const create = safe(async () => { await post('/api/inbounds', createForm.value); creating.value = false; emit('toast', t('protocol.created')); await load() })
const save = safe(async () => { if (!editing.value) return; await put(`/api/inbounds/${editing.value.id}`, editing.value); editing.value = null; emit('toast', t('protocol.saved')); await load() })
const toggle = safe(async (item: Inbound) => { await put(`/api/inbounds/${item.id}`, { ...item, enabled: !item.enabled, link: undefined, network: undefined }); emit('toast', item.enabled ? t('protocol.disabled') : t('protocol.enabled')); await load() })
const remove = safe(async (item: Inbound) => { await del(`/api/inbounds/${item.id}`); emit('toast', t('protocol.deleted')); await load() })
const copy = safe(async (value: string) => { await navigator.clipboard.writeText(value); emit('toast', t('protocol.linkCopied')) })
const showQR = safe(async (item: LinkNode) => { qrItem.value = item; qr.value = await QRCode.toDataURL(item.link, { width: 320, margin: 2, color: { dark: '#111712', light: '#f4f1e8' } }) })
function edit(item: Inbound) { editing.value = JSON.parse(JSON.stringify(item)); delete (editing.value as any).link; delete (editing.value as any).network }
function openCreate() { createForm.value = { type: 'vless-reality', name: t('protocol.defaultName'), port: 8443 }; creating.value = true }
onMounted(load)
</script>

<template>
  <div class="page">
    <header class="page-head"><div><span class="eyebrow">INBOUND ROUTES</span><h1>{{ t('protocol.title') }}</h1><p>{{ t('protocol.help') }}</p></div><button class="primary" @click="openCreate"><Icon name="plus"/>{{ t('protocol.add') }}</button></header>
    <div class="protocol-grid">
      <template v-for="item in items" :key="item.id">
        <article class="protocol-card" :class="{ disabled: !item.enabled }">
          <div class="protocol-top"><div class="protocol-glyph">{{ item.type === 'vless-reality' ? 'VL' : 'H2' }}</div><div><span class="pill">{{ item.type === 'vless-reality' ? 'VLESS · REALITY' : 'HYSTERIA2' }}</span><h2>{{ item.name }}</h2></div><SwitchRoot :model-value="item.enabled" class="relative h-6 w-11 rounded-full border border-[var(--ink)] bg-transparent p-[3px] data-[state=checked]:bg-[var(--signal)]" :aria-label="item.enabled ? t('protocol.disable') : t('protocol.enable')" @update:model-value="toggle(item)"><SwitchThumb class="block size-4 rounded-full bg-[var(--muted)] transition-transform data-[state=checked]:translate-x-[18px] data-[state=checked]:bg-[var(--ink)]" /></SwitchRoot></div>
          <div class="endpoint"><span>{{ item.network.toUpperCase() }}</span><code>{{ item.port }}</code><b>{{ item.enabled ? 'ACTIVE' : 'STOPPED' }}</b></div>
          <dl v-if="item.vless"><div><dt>SNI</dt><dd>{{ item.vless.sni }}</dd></div><div><dt>FLOW</dt><dd>XTLS Vision</dd></div><div><dt>SHORT ID</dt><dd>{{ item.vless.shortId }}</dd></div></dl>
          <dl v-else><div><dt>TLS</dt><dd>Server certificate</dd></div><div><dt>ALPN</dt><dd>h3</dd></div><div><dt>OBFS</dt><dd>{{ item.hysteria2?.obfs || t('protocol.off') }}</dd></div></dl>
          <div class="card-actions"><button @click="copy(item.link)"><Icon name="copy"/>{{ t('protocol.copyLink') }}</button><button :aria-label="t('protocol.showQr')" @click="showQR(item)">QR</button><button :aria-label="t('protocol.edit')" @click="edit(item)"><Icon name="edit"/></button><ConfirmAction :title="item.name" :message="t('protocol.deleteConfirm', { name: item.name })" destructive @confirm="remove(item)"><button class="destructive" :aria-label="t('protocol.delete')"><Icon name="trash"/></button></ConfirmAction></div>
        </article>
        <article v-if="item.wireGuardNode" class="protocol-card companion-card">
          <div class="protocol-top"><div class="protocol-glyph">WG</div><div><span class="pill">WIREGUARD · IPv4</span><h2>{{ item.wireGuardNode.name }}</h2></div><span class="companion-managed"><Icon name="route"/>{{ t('protocol.companionManaged') }}</span></div>
          <div class="endpoint"><span>{{ item.network.toUpperCase() }}</span><code>{{ item.port }}</code><b>ACTIVE</b></div>
          <dl><div><dt>{{ t('protocol.companionEntry') }}</dt><dd>{{ item.name }}</dd></div><div><dt>{{ t('protocol.companionEgress') }}</dt><dd>WireGuard IPv4</dd></div><div><dt>{{ t('protocol.companionControl') }}</dt><dd>{{ t('protocol.companionSettings') }}</dd></div></dl>
          <div class="card-actions"><button @click="copy(item.wireGuardNode.link)"><Icon name="copy"/>{{ t('protocol.copyLink') }}</button><button :aria-label="t('protocol.showQr')" @click="showQR(item.wireGuardNode)">QR</button></div>
        </article>
      </template>
      <button class="add-card" @click="openCreate"><Icon name="plus"/><strong>{{ t('protocol.addAnother') }}</strong><span>{{ t('protocol.addAnotherHelp') }}</span></button>
    </div>

    <Modal v-if="creating" :title="t('protocol.newTitle')" :description="t('protocol.addAnotherHelp')" @close="creating = false">
      <form class="form-grid" @submit.prevent="create">
        <label>{{ t('protocol.type') }}<SelectControl v-model="createForm.type" :options="protocolOptions" /></label>
        <div v-if="duplicate" class="alert warning">{{ t('protocol.duplicate') }}</div>
        <label>{{ t('protocol.name') }}<input v-model.trim="createForm.name" maxlength="80" required></label>
        <label>{{ t('protocol.port') }}<input v-model.number="createForm.port" type="number" min="1" max="65535" required><small>{{ t('protocol.portHelp', { network: createForm.type === 'vless-reality' ? 'TCP' : 'UDP' }) }}</small></label>
        <div class="modal-actions"><button type="button" class="secondary" @click="creating = false">{{ t('protocol.cancel') }}</button><button class="primary">{{ t('protocol.generate') }}</button></div>
      </form>
    </Modal>

    <Modal v-if="editing" :title="t('protocol.editTitle')" :description="t('protocol.firewall')" wide @close="editing = null">
      <form class="form-grid two" @submit.prevent="save">
        <label>{{ t('protocol.name') }}<input v-model.trim="editing.name" maxlength="80" required></label><label>{{ t('protocol.port') }}<input v-model.number="editing.port" type="number" min="1" max="65535" required></label>
        <template v-if="editing.vless"><label>UUID<input v-model="editing.vless.uuid" required></label><label>Reality SNI<input v-model.trim="editing.vless.sni" required></label><label>{{ t('protocol.privateKey') }}<PasswordInput v-model="editing.vless.privateKey" :aria-label="t('protocol.privateKey')" autocomplete="off" required /></label><label>{{ t('protocol.publicKey') }}<input v-model="editing.vless.publicKey" required></label><label>Short ID<input v-model="editing.vless.shortId" required></label></template>
        <template v-if="editing.hysteria2"><label>{{ t('protocol.password') }}<PasswordInput v-model="editing.hysteria2.password" :aria-label="t('protocol.password')" autocomplete="off" minlength="8" required /></label><label>{{ t('protocol.obfs') }}<SelectControl :model-value="editing.hysteria2.obfs || 'none'" :options="obfsOptions" @update:model-value="value => { if (editing?.hysteria2) editing.hysteria2.obfs = value === 'none' ? '' : value }" /></label><label v-if="editing.hysteria2.obfs" class="span-two">{{ t('protocol.obfsPassword') }}<PasswordInput v-model="editing.hysteria2.obfsPassword" :aria-label="t('protocol.obfsPassword')" autocomplete="off" minlength="8" required /></label></template>
        <div class="alert subtle span-two">{{ t('protocol.firewall') }}</div>
        <div class="modal-actions span-two"><button type="button" class="secondary" @click="editing = null">{{ t('protocol.cancel') }}</button><button class="primary">{{ t('protocol.apply') }}</button></div>
      </form>
    </Modal>
    <Modal v-if="qrItem" :title="qrItem.name" :description="t('protocol.qrHelp')" @close="qrItem = null"><div class="large-qr"><img :src="qr" :alt="t('protocol.qrAlt')"><p>{{ t('protocol.qrHelp') }}</p></div></Modal>
  </div>
</template>

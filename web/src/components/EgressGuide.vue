<script setup lang="ts">
import { t } from '../i18n'

const installCommands = 'sudo apt update\nsudo apt install -y wireguard iptables iproute2\nip route show default'
const keyCommands = [
  'sudo install -d -m 700 /etc/wireguard',
  "sudo sh -c 'umask 077; test -s /etc/wireguard/sbm-egress-private.key || wg genkey > /etc/wireguard/sbm-egress-private.key; wg pubkey < /etc/wireguard/sbm-egress-private.key > /etc/wireguard/sbm-egress-public.key'",
  'sudo cat /etc/wireguard/sbm-egress-public.key',
].join('\n')
const forwardingCommands = "echo 'net.ipv4.ip_forward=1' | sudo tee /etc/sysctl.d/70-sbm-egress.conf\nsudo sysctl -p /etc/sysctl.d/70-sbm-egress.conf\nsudo nano /etc/wireguard/sbm-egress.conf"
const startCommands = 'sudo chmod 600 /etc/wireguard/sbm-egress.conf /etc/wireguard/sbm-egress-private.key\nsudo systemctl enable --now wg-quick@sbm-egress\nsudo wg show sbm-egress'
const config = [
  '[Interface]',
  'Address = 10.66.X.1/24',
  'ListenPort = 51820',
  'MTU = 1408',
  'PostUp = wg set %i private-key /etc/wireguard/sbm-egress-private.key',
  'PostUp = iptables -A FORWARD -i %i -o B_PUBLIC_INTERFACE -j ACCEPT',
  'PostUp = iptables -A FORWARD -i B_PUBLIC_INTERFACE -o %i -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT',
  'PostUp = iptables -t nat -A POSTROUTING -s 10.66.X.0/24 -o B_PUBLIC_INTERFACE -j MASQUERADE',
  'PostDown = iptables -D FORWARD -i %i -o B_PUBLIC_INTERFACE -j ACCEPT',
  'PostDown = iptables -D FORWARD -i B_PUBLIC_INTERFACE -o %i -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT',
  'PostDown = iptables -t nat -D POSTROUTING -s 10.66.X.0/24 -o B_PUBLIC_INTERFACE -j MASQUERADE',
  '',
  '[Peer]',
  'PublicKey = A_PUBLIC_KEY',
  'AllowedIPs = 10.66.X.2/32',
].join('\n')
</script>

<template>
  <details class="egress-guide">
    <summary><span class="guide-document-icon" aria-hidden="true">WG</span><span><strong>{{ t('egress.guide.title') }}</strong><small>{{ t('egress.guide.subtitle') }}</small></span><span class="guide-toggle" aria-hidden="true">+</span></summary>
    <div class="guide-content">
      <p class="guide-intro">{{ t('egress.guide.intro') }}</p>
      <ol class="guide-steps">
        <li>
          <div class="guide-step-head"><span aria-hidden="true">01</span><h3>{{ t('egress.guide.installTitle') }}</h3></div>
          <p>{{ t('egress.guide.installHelp') }}</p>
          <div class="guide-code"><div><span>B · SSH</span></div><pre><code>{{ installCommands }}</code></pre></div>
          <p>{{ t('egress.guide.interfaceHelp') }}</p>
        </li>
        <li>
          <div class="guide-step-head"><span aria-hidden="true">02</span><h3>{{ t('egress.guide.keysTitle') }}</h3></div>
          <p>{{ t('egress.guide.keysHelp') }}</p>
          <div class="guide-code"><div><span>B · SSH</span></div><pre><code>{{ keyCommands }}</code></pre></div>
        </li>
        <li>
          <div class="guide-step-head"><span aria-hidden="true">03</span><h3>{{ t('egress.guide.panelTitle') }}</h3></div>
          <p>{{ t('egress.guide.panelHelp') }}</p>
          <dl class="guide-fields"><div><dt>{{ t('egress.exitIP') }}</dt><dd>{{ t('egress.guide.ipHelp') }}</dd></div><div><dt>{{ t('egress.privateKey') }}</dt><dd>{{ t('egress.guide.aKeyHelp') }}</dd></div><div><dt>{{ t('egress.peerKey') }}</dt><dd>{{ t('egress.guide.bKeyHelp') }}</dd></div></dl>
          <p>{{ t('egress.guide.draftHelp') }}</p>

        </li>
        <li>
          <div class="guide-step-head"><span aria-hidden="true">04</span><h3>{{ t('egress.guide.configTitle') }}</h3></div>
          <p>{{ t('egress.guide.configHelp') }}</p>
          <dl class="guide-fields"><div><dt><code>X</code></dt><dd>{{ t('egress.guide.slotHelp') }}</dd></div><div><dt><code>A_PUBLIC_KEY</code></dt><dd>{{ t('egress.guide.aPublicKeyHelp') }}</dd></div><div><dt><code>B_PUBLIC_INTERFACE</code></dt><dd>{{ t('egress.guide.interfaceHelp') }}</dd></div><div><dt><code>ListenPort</code></dt><dd>{{ t('egress.guide.ipHelp') }}</dd></div></dl>
          <div class="guide-code"><div><span>B · SSH</span></div><pre><code>{{ forwardingCommands }}</code></pre></div>
          <div class="guide-code"><div><span>/etc/wireguard/sbm-egress.conf</span></div><pre><code>{{ config }}</code></pre></div>
          <p class="guide-notice">{{ t('egress.guide.configNotice') }}</p>
          <p>{{ t('egress.guide.firewallHelp', { port: 51820 }) }}</p>
          <div class="guide-code"><div><span>B · SSH</span></div><pre><code>{{ startCommands }}</code></pre></div>
          <p>{{ t('egress.guide.restartHelp') }}</p>
        </li>
        <li>
          <div class="guide-step-head"><span aria-hidden="true">05</span><h3>{{ t('egress.guide.verifyTitle') }}</h3></div>
          <p>{{ t('egress.guide.verifyHelp') }}</p>
          <p><a href="https://cloudflare.com/cdn-cgi/trace" target="_blank" rel="noopener noreferrer">cloudflare.com/cdn-cgi/trace ↗</a></p>
          <p>{{ t('egress.guide.troubleshoot') }}</p>
        </li>
      </ol>
      <a class="guide-reference" href="https://www.wireguard.com/quickstart/" target="_blank" rel="noopener noreferrer">{{ t('egress.guide.reference') }} ↗</a>
    </div>
  </details>
</template>

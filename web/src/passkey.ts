import { api } from './api'

// Browser side of passkeys: the server sends options with base64url strings; the WebAuthn API
// wants ArrayBuffers, and its answer has to go back as base64url JSON.

const fromB64 = (s: string) => Uint8Array.from(atob(s.replace(/-/g, '+').replace(/_/g, '/')), (c) => c.charCodeAt(0)).buffer
const toB64 = (b: ArrayBuffer) =>
  btoa(String.fromCharCode(...new Uint8Array(b)))
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=+$/, '')

export const passkeysSupported = () => typeof window.PublicKeyCredential !== 'undefined' && window.isSecureContext

type Json = Record<string, any> // eslint-disable-line @typescript-eslint/no-explicit-any

async function post(path: string, body: unknown) {
  const res = await fetch('/api' + path, {
    method: 'POST',
    headers: { 'X-OSSR': '1', 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    credentials: 'same-origin',
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) throw new Error(data.error || res.statusText)
  return data
}

function friendly(e: unknown): Error {
  const err = e as Error
  if (err?.name === 'NotAllowedError') return new Error('Cancelled, or the passkey prompt timed out.')
  if (err?.name === 'InvalidStateError') return new Error('This device already has a passkey here.')
  if (err?.name === 'SecurityError') return new Error('Passkeys need HTTPS (or localhost) and a hostname, not an IP address.')
  return err
}

export async function addPasskey(name: string) {
  const { publicKey: o } = await api<{ publicKey: Json }>('/passkeys/register/begin', { method: 'POST', body: { name } })
  o.challenge = fromB64(o.challenge)
  o.user.id = fromB64(o.user.id)
  o.excludeCredentials = (o.excludeCredentials ?? []).map((c: Json) => ({ ...c, id: fromB64(c.id) }))
  let cred: PublicKeyCredential
  try {
    cred = (await navigator.credentials.create({ publicKey: o as PublicKeyCredentialCreationOptions })) as PublicKeyCredential
  } catch (e) {
    throw friendly(e)
  }
  const r = cred.response as AuthenticatorAttestationResponse
  return post('/passkeys/register/finish', {
    id: cred.id,
    rawId: toB64(cred.rawId),
    type: cred.type,
    response: { clientDataJSON: toB64(r.clientDataJSON), attestationObject: toB64(r.attestationObject), transports: r.getTransports?.() ?? [] },
  })
}

export async function signInWithPasskey() {
  const { publicKey: o } = await post('/passkeys/login/begin', {})
  o.challenge = fromB64(o.challenge)
  o.allowCredentials = (o.allowCredentials ?? []).map((c: Json) => ({ ...c, id: fromB64(c.id) }))
  let cred: PublicKeyCredential
  try {
    cred = (await navigator.credentials.get({ publicKey: o as PublicKeyCredentialRequestOptions })) as PublicKeyCredential
  } catch (e) {
    throw friendly(e)
  }
  const r = cred.response as AuthenticatorAssertionResponse
  return post('/passkeys/login/finish', {
    id: cred.id,
    rawId: toB64(cred.rawId),
    type: cred.type,
    response: {
      clientDataJSON: toB64(r.clientDataJSON),
      authenticatorData: toB64(r.authenticatorData),
      signature: toB64(r.signature),
      userHandle: r.userHandle ? toB64(r.userHandle) : null,
    },
  })
}

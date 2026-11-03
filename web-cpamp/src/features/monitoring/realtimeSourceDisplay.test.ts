import { describe, expect, it } from 'vitest';
import type { TFunction } from 'i18next';
import { buildRealtimeSourceDisplay } from './realtimeSourceDisplay';

const labels: Record<string, string> = {
  'monitoring.filter_provider': 'Provider',
  'monitoring.column_host': 'Host',
  'monitoring.source': 'Source',
  'monitoring.client_ip': 'Client IP',
  'monitoring.x_forwarded_for_unverified': 'Forwarded chain (unverified)',
  'monitoring.user_agent': 'User-Agent',
  'monitoring.generate': 'Generate',
  'monitoring.stream': 'Stream',
  'common.yes': 'Yes',
  'common.no': 'No',
};

const t = ((key: string) => labels[key] || key) as TFunction;

const row = {
  account: 'alice@example.com',
  accountMasked: 'ali***@example.com',
  authLabel: 'alice',
  channel: 'codex',
  channelHost: 'api.openai.com',
  clientIp: '192.0.2.10',
  provider: 'codex',
  source: 'alice@example.com',
  sourceMasked: 'ali***@example.com',
  userAgent: 'test-client/1.0',
  xForwardedFor: '203.0.113.5, 198.51.100.8',
};

describe('buildRealtimeSourceDisplay request metadata', () => {
  it('does not render request metadata in masked mode', () => {
    const display = buildRealtimeSourceDisplay(row, t, 'masked');

    expect(display.requestMetadataTitle).toBe('');
    expect(display.title).not.toContain('alice@example.com');
    expect(display.title).not.toContain('192.0.2.10');
    expect(display.title).not.toContain('203.0.113.5');
    expect(display.title).not.toContain('test-client/1.0');
  });

  it('renders labeled request metadata in full mode', () => {
    const display = buildRealtimeSourceDisplay(row, t, 'full');

    expect(display.requestMetadataTitle).toBe(
      [
        'Client IP: 192.0.2.10',
        'Forwarded chain (unverified): 203.0.113.5, 198.51.100.8',
        'User-Agent: test-client/1.0',
      ].join('\n')
    );
    expect(display.title).toContain('Client IP: 192.0.2.10');
    expect(display.title).toContain(
      'Forwarded chain (unverified): 203.0.113.5, 198.51.100.8'
    );
    expect(display.title).toContain('User-Agent: test-client/1.0');
    expect(display.requestMetadataTitle).not.toContain('Generate:');
    expect(display.requestMetadataTitle).not.toContain('Stream:');
  });

  it.each(['masked', 'full'] as const)('renders strict boolean metadata in %s mode', (mode) => {
    const display = buildRealtimeSourceDisplay({ ...row, generate: true, stream: false }, t, mode);

    expect(display.requestMetadataTitle).toContain('Generate: Yes\nStream: No');
    expect(display.title).toContain('Generate: Yes');
    expect(display.title).toContain('Stream: No');
    if (mode === 'masked') {
      expect(display.requestMetadataTitle).toBe('Generate: Yes\nStream: No');
      expect(display.title).not.toContain('192.0.2.10');
      expect(display.title).not.toContain('203.0.113.5');
      expect(display.title).not.toContain('test-client/1.0');
    } else {
      expect(display.requestMetadataTitle).toContain('Client IP: 192.0.2.10');
      expect(display.requestMetadataTitle).toContain('Forwarded chain (unverified):');
      expect(display.requestMetadataTitle).toContain('User-Agent: test-client/1.0');
    }
  });

  it('keeps false distinct from an absent boolean', () => {
    const generateOnly = buildRealtimeSourceDisplay({ ...row, generate: false }, t);
    const streamOnly = buildRealtimeSourceDisplay({ ...row, stream: false }, t);

    expect(generateOnly.requestMetadataTitle).toBe('Generate: No');
    expect(streamOnly.requestMetadataTitle).toBe('Stream: No');
  });

  it.each([null, 'true', 'false', '', 0, 1, {}, []])('ignores non-boolean metadata: %j', (value) => {
    const invalidRow = {
      ...row,
      generate: value,
      stream: value,
    } as unknown as Parameters<typeof buildRealtimeSourceDisplay>[0];

    for (const mode of ['masked', 'full'] as const) {
      const display = buildRealtimeSourceDisplay(invalidRow, t, mode);
      expect(display.requestMetadataTitle).not.toContain('Generate:');
      expect(display.requestMetadataTitle).not.toContain('Stream:');
      expect(display.title).not.toContain('Generate:');
      expect(display.title).not.toContain('Stream:');
    }
  });
});

const opaqueSources = [
  `h:${'0123456789abcdef'.repeat(4)}`,
  'k:0123456789abcdef',
  'm:sk-1...cdef',
];

describe('buildRealtimeSourceDisplay source priority', () => {
  it.each(opaqueSources)('prefers a readable account over an opaque source: %s', (source) => {
    const display = buildRealtimeSourceDisplay(
      { ...row, source, sourceMasked: source, channelHost: '' },
      t
    );

    expect(display.primary).toBe('ali***@example.com');
    expect(display.meta).toBe('Provider: codex');
  });

  it.each(opaqueSources)('uses an opaque source only when readable metadata is absent: %s', (source) => {
    const display = buildRealtimeSourceDisplay(
      {
        source,
        sourceMasked: source,
        account: '',
        accountMasked: '',
        authLabel: '',
        channel: '',
        channelHost: '',
        provider: '',
      },
      t
    );

    expect(display.primary).toBe(source);
  });

  it.each(['devin', 'workbuddy'])('prefers an account to provider-equivalent %s labels', (provider) => {
    for (const mode of ['masked', 'full'] as const) {
      const display = buildRealtimeSourceDisplay(
        {
          ...row,
          authLabel: provider,
          source: provider,
          sourceMasked: provider,
          channel: provider,
          channelHost: '',
          provider,
        },
        t,
        mode
      );

      expect(display.primary).toBe(mode === 'full' ? 'alice@example.com' : 'ali***@example.com');
      expect(display.meta).toBe(`Provider: ${provider}`);
    }
  });

  it('keeps a distinct custom channel ahead of the account', () => {
    const display = buildRealtimeSourceDisplay(
      { ...row, channel: 'Team Relay', channelHost: '', provider: 'workbuddy' },
      t
    );

    expect(display.primary).toBe('Team Relay');
  });

  it('preserves key-disambiguated source names', () => {
    const display = buildRealtimeSourceDisplay(
      {
        ...row,
        channel: 'workbuddy',
        channelHost: '',
        provider: 'workbuddy',
        source: 'workbuddy #1',
        sourceMasked: 'workbuddy #1',
      },
      t
    );

    expect(display.primary).toBe('workbuddy #1');
  });
});

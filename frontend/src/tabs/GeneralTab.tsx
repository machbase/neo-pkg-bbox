import { useState } from 'react';
import type { GeneralSettings } from '../types/settings';

type GeneralTabProps = {
  settings: GeneralSettings;
  onChange: (next: GeneralSettings) => void;
};

type FieldProps = {
  id: string;
  label: string;
  value: string | number;
  onChange: (value: string) => void;
  type?: 'text' | 'password' | 'number';
  disabled?: boolean;
};

function Field({ id, label, value, onChange, type = 'text', disabled = false }: FieldProps) {
  return (
    <div className="field-row">
      <label htmlFor={id}>{label}</label>
      <input
        id={id}
        name={id}
        type={type}
        value={value}
        disabled={disabled}
        onChange={(event) => onChange(event.target.value)}
      />
    </div>
  );
}

function toNumber(raw: string, fallback: number): number {
  const parsed = Number(raw);
  return Number.isNaN(parsed) ? fallback : parsed;
}

export function GeneralTab({ settings, onChange }: GeneralTabProps) {
  const [showToken, setShowToken] = useState(false);

  return (
    <section id="panel-general" role="tabpanel" aria-labelledby="tab-general" className="tab-panel">
      <div className="card-grid">
        <article className="panel-card">
          <h3>Server</h3>
          <Field
            id="server-address"
            label="Address"
            value={settings.server.address}
            disabled
            onChange={(value) =>
              onChange({
                ...settings,
                server: {
                  ...settings.server,
                  address: value,
                },
              })}
          />
          <Field
            id="server-camera-dir"
            label="Camera Directory"
            value={settings.server.cameraDirectory}
            onChange={(value) =>
              onChange({
                ...settings,
                server: {
                  ...settings.server,
                  cameraDirectory: value,
                },
              })}
          />
          <Field
            id="server-mvs-dir"
            label="MVS Directory"
            value={settings.server.mvsDirectory}
            onChange={(value) =>
              onChange({
                ...settings,
                server: {
                  ...settings.server,
                  mvsDirectory: value,
                },
              })}
          />
          <Field
            id="server-data-dir"
            label="Data Directory"
            value={settings.server.dataDirectory}
            onChange={(value) =>
              onChange({
                ...settings,
                server: {
                  ...settings.server,
                  dataDirectory: value,
                },
              })}
          />
        </article>

        <article className="panel-card">
          <h3>Machbase</h3>
          <Field
            id="machbase-host"
            label="Host"
            value={settings.machbase.host}
            onChange={(value) =>
              onChange({
                ...settings,
                machbase: {
                  ...settings.machbase,
                  host: value,
                },
              })}
          />
          <Field
            id="machbase-port"
            label="Port"
            type="number"
            value={settings.machbase.port}
            onChange={(value) =>
              onChange({
                ...settings,
                machbase: {
                  ...settings.machbase,
                  port: toNumber(value, settings.machbase.port),
                },
              })}
          />
          <Field
            id="machbase-database"
            label="Database"
            value={settings.machbase.database}
            onChange={(value) =>
              onChange({
                ...settings,
                machbase: {
                  ...settings.machbase,
                  database: value,
                },
              })}
          />
          <Field
            id="machbase-timeout"
            label="Timeout Seconds"
            type="number"
            value={settings.machbase.timeoutSeconds}
            onChange={(value) =>
              onChange({
                ...settings,
                machbase: {
                  ...settings.machbase,
                  timeoutSeconds: toNumber(value, settings.machbase.timeoutSeconds),
                },
              })}
          />
          <label className="toggle-row" htmlFor="machbase-use-token">
            <div>
              <p className="toggle-title">Use Token</p>
              <p className="field-hint">Enable token-based authentication for Machbase requests.</p>
            </div>
            <span className="toggle-control">
              <input
                id="machbase-use-token"
                name="machbase-use-token"
                type="checkbox"
                checked={settings.machbase.useToken}
                onChange={(event) => {
                  const nextUseToken = event.target.checked;
                  if (!nextUseToken) {
                    setShowToken(false);
                  }
                  onChange({
                    ...settings,
                    machbase: {
                      ...settings.machbase,
                      useToken: nextUseToken,
                    },
                  });
                }}
              />
              <span className="toggle-slider" />
            </span>
          </label>
          {settings.machbase.useToken ? (
            <div className="field-row">
              <label htmlFor="machbase-api-token">Token</label>
              <div className="token-input-wrap">
                <input
                  id="machbase-api-token"
                  name="machbase-api-token"
                  type={showToken ? 'text' : 'password'}
                  value={settings.machbase.apiToken}
                  onChange={(event) =>
                    onChange({
                      ...settings,
                      machbase: {
                        ...settings.machbase,
                        apiToken: event.target.value,
                      },
                    })}
                />
                <button
                  type="button"
                  className="btn btn-ghost btn-inline-action"
                  onClick={() => setShowToken((current) => !current)}
                >
                  {showToken ? 'Hide' : 'Show'}
                </button>
              </div>
            </div>
          ) : null}
        </article>

        <article className="panel-card">
          <h3>MediaMTX</h3>
          <Field
            id="mediamtx-host"
            label="Host"
            value={settings.mediaMtx.host}
            onChange={(value) =>
              onChange({
                ...settings,
                mediaMtx: {
                  ...settings.mediaMtx,
                  host: value,
                },
              })}
          />
          <Field
            id="mediamtx-port"
            label="Port"
            type="number"
            value={settings.mediaMtx.port}
            onChange={(value) =>
              onChange({
                ...settings,
                mediaMtx: {
                  ...settings.mediaMtx,
                  port: toNumber(value, settings.mediaMtx.port),
                },
              })}
          />
          <Field
            id="mediamtx-binary"
            label="Binary"
            value={settings.mediaMtx.binary}
            onChange={(value) =>
              onChange({
                ...settings,
                mediaMtx: {
                  ...settings.mediaMtx,
                  binary: value,
                },
              })}
          />
        </article>

        <article className="panel-card">
          <h3>FFmpeg</h3>
          <Field
            id="ffmpeg-binary"
            label="Binary"
            value={settings.ffmpeg.binary}
            onChange={(value) =>
              onChange({
                ...settings,
                ffmpeg: {
                  ...settings.ffmpeg,
                  binary: value,
                },
              })}
          />
          <Field
            id="ffmpeg-probe-binary"
            label="FFprobe Binary"
            value={settings.ffmpeg.probeBinary}
            onChange={(value) =>
              onChange({
                ...settings,
                ffmpeg: {
                  ...settings.ffmpeg,
                  probeBinary: value,
                },
              })}
          />
        </article>
      </div>
    </section>
  );
}

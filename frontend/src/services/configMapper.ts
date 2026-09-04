import type { ApiConfigData, ApiConfigPostBody } from '../types/configApi';
import type { SettingsDraft } from '../types/settings';

function cloneApiConfig(data: ApiConfigData): ApiConfigData {
  return JSON.parse(JSON.stringify(data)) as ApiConfigData;
}

function createProbeArgId(index: number): string {
  return `probe-${index}-${Math.random().toString(16).slice(2, 8)}`;
}

function isLogLevel(value: string): value is SettingsDraft['log']['logLevel'] {
  return value === 'debug' || value === 'info' || value === 'warn' || value === 'error';
}

function isLogFormat(value: string): value is SettingsDraft['log']['logFormat'] {
  return value === 'json' || value === 'text';
}

function isLogOutput(value: string): value is SettingsDraft['log']['outputDestination'] {
  return value === 'stdout' || value === 'file' || value === 'both';
}

function splitFilename(filename: string): { prefix: string; extension: string } {
  const index = filename.lastIndexOf('.');
  if (index <= 0 || index >= filename.length - 1) {
    return { prefix: filename || 'blackbox', extension: 'log' };
  }
  return {
    prefix: filename.slice(0, index),
    extension: filename.slice(index + 1),
  };
}

function readProbeFlag(item: ApiConfigData['ffmpeg']['defaults']['probe_args'][number]): string {
  return item.flag ?? item.Flag ?? '';
}

function readProbeValue(item: ApiConfigData['ffmpeg']['defaults']['probe_args'][number]): string {
  return item.value ?? item.Value ?? '';
}

function hasUppercaseProbeKeys(
  probeArgs: ApiConfigData['ffmpeg']['defaults']['probe_args'],
): boolean {
  return probeArgs.some((item) => 'Flag' in item || 'Value' in item);
}

export function buildFallbackApiConfigData(): ApiConfigData {
  return {
    server: {
      addr: '0.0.0.0:8000',
      camera_dir: '../bin/cameras',
      mvs_dir: '../ai/mvs',
      data_dir: '../bin/data',
    },
    machbase: {
      scheme: 'http',
      host: '127.0.0.1',
      port: 5654,
      database: 'MACHBASEDB',
      timeout_seconds: 30,
      token: '',
    },
    ffmpeg: {
      binary: '../tools/ffmpeg',
      defaults: {
        probe_binary: '../tools/ffprobe',
        probe_args: [
          { flag: 'v', value: 'error' },
          { flag: 'select_streams', value: 'v:0' },
          { flag: 'show_entries', value: 'packet=pts_time,duration_time' },
          { flag: 'of', value: 'csv=p=0' },
        ],
      },
    },
    mediamtx: {
      binary: '../tools/mediamtx',
      config_file: '../tools/mediamtx.yml',
      host: '127.0.0.1',
      webrtc_host: '',
      port: 9997,
      webrtc_port: 0,
      rtsp_server_port: 0,
    },
    log: {
      dir: '../logs',
      level: 'info',
      format: 'json',
      output: 'both',
      file: {
        filename: 'blackbox.log',
        max_size: 100,
        max_backups: 10,
        max_age: 30,
        compress: true,
      },
    },
    ai: {
      binary: '../ai/blackbox-ai-manager',
      config_file: '../ai/config.json',
    },
  };
}

export function fromApiToDraft(api: ApiConfigData): { draft: SettingsDraft; shadow: ApiConfigData } {
  const filename = splitFilename(api.log.file.filename);
  const token = api.machbase.token || '';

  const draft: SettingsDraft = {
    general: {
      server: {
        address: api.server.addr,
        cameraDirectory: api.server.camera_dir,
        mvsDirectory: api.server.mvs_dir,
        dataDirectory: api.server.data_dir,
      },
      machbase: {
        host: api.machbase.host,
        port: api.machbase.port,
        database: api.machbase.database || 'MACHBASEDB',
        timeoutSeconds: api.machbase.timeout_seconds,
        useToken: token.trim() !== '',
        apiToken: token,
      },
      mediaMtx: {
        host: api.mediamtx.host,
        port: api.mediamtx.port,
        binary: api.mediamtx.binary,
      },
      ffmpeg: {
        binary: api.ffmpeg.binary,
        probeBinary: api.ffmpeg.defaults.probe_binary,
      },
    },
    ffmpeg: {
      probeArgs: api.ffmpeg.defaults.probe_args.map((item, index) => ({
        id: createProbeArgId(index),
        flag: readProbeFlag(item),
        value: readProbeValue(item),
      })),
    },
    log: {
      logDirectory: api.log.dir,
      logLevel: isLogLevel(api.log.level) ? api.log.level : 'info',
      logFormat: isLogFormat(api.log.format) ? api.log.format : 'json',
      outputDestination: isLogOutput(api.log.output) ? api.log.output : 'both',
      filenamePrefix: filename.prefix,
      filenameExtension: filename.extension,
      maxFileSizeMb: api.log.file.max_size,
      maxBackups: api.log.file.max_backups,
      maxAgeDays: api.log.file.max_age,
      compressOldLogs: api.log.file.compress,
    },
  };

  return {
    draft,
    shadow: cloneApiConfig(api),
  };
}

export function toPostPayload(draft: SettingsDraft, shadow: ApiConfigData): ApiConfigPostBody {
  const payload = cloneApiConfig(shadow);
  const uppercaseProbeKeys = hasUppercaseProbeKeys(shadow.ffmpeg.defaults.probe_args);

  payload.server.addr = draft.general.server.address;
  payload.server.camera_dir = draft.general.server.cameraDirectory;
  payload.server.mvs_dir = draft.general.server.mvsDirectory;
  payload.server.data_dir = draft.general.server.dataDirectory;

  payload.machbase.host = draft.general.machbase.host;
  payload.machbase.port = draft.general.machbase.port;
  payload.machbase.database = draft.general.machbase.database || 'MACHBASEDB';
  payload.machbase.timeout_seconds = draft.general.machbase.timeoutSeconds;
  payload.machbase.token = draft.general.machbase.useToken ? draft.general.machbase.apiToken : '';

  payload.ffmpeg.binary = draft.general.ffmpeg.binary;
  payload.ffmpeg.defaults.probe_binary = draft.general.ffmpeg.probeBinary;
  payload.ffmpeg.defaults.probe_args = draft.ffmpeg.probeArgs.map((item) =>
    uppercaseProbeKeys
      ? {
          Flag: item.flag,
          Value: item.value,
        }
      : {
          flag: item.flag,
          value: item.value,
        },
  );

  payload.mediamtx.host = draft.general.mediaMtx.host;
  payload.mediamtx.port = draft.general.mediaMtx.port;
  payload.mediamtx.binary = draft.general.mediaMtx.binary;

  payload.log.dir = draft.log.logDirectory;
  payload.log.level = draft.log.logLevel;
  payload.log.format = draft.log.logFormat;
  payload.log.output = draft.log.outputDestination;
  payload.log.file.filename = `${draft.log.filenamePrefix}.log`;
  payload.log.file.max_size = draft.log.maxFileSizeMb;
  payload.log.file.max_backups = draft.log.maxBackups;
  payload.log.file.max_age = draft.log.maxAgeDays;
  payload.log.file.compress = draft.log.compressOldLogs;

  return payload;
}

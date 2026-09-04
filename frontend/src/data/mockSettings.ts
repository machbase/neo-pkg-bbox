import type { FFmpegDefaults, GeneralSettings, LogSettings } from '../types/settings';

export const generalSettings: GeneralSettings = {
  server: {
    address: '0.0.0.0:8000',
    cameraDirectory: '../bin/cameras',
    mvsDirectory: '../ai/mvs',
    dataDirectory: '../bin/data',
  },
  machbase: {
    host: '127.0.0.1',
    port: 5654,
    database: 'MACHBASEDB',
    timeoutSeconds: 30,
    useToken: false,
    apiToken: '',
  },
  mediaMtx: {
    host: '127.0.0.1',
    port: 9997,
    binary: '../tools/mediamtx',
  },
  ffmpeg: {
    binary: '../tools/ffmpeg',
    probeBinary: '../tools/ffprobe',
  },
};

export const ffmpegDefaults: FFmpegDefaults = {
  probeArgs: [
    { id: 'arg-1', flag: 'v', value: 'error' },
    { id: 'arg-2', flag: 'select_streams', value: 'v:0' },
    { id: 'arg-3', flag: 'show_entries', value: 'packet=pts_time,duration_time' },
    { id: 'arg-4', flag: 'of', value: 'csv=p=0' },
  ],
};

export const logSettings: LogSettings = {
  logDirectory: '../logs',
  logLevel: 'info',
  logFormat: 'json',
  outputDestination: 'both',
  filenamePrefix: 'blackbox',
  filenameExtension: 'log',
  maxFileSizeMb: 100,
  maxBackups: 10,
  maxAgeDays: 30,
  compressOldLogs: true,
};

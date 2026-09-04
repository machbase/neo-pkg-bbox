export interface ApiEnvelope<T> {
  success: boolean;
  reason: string;
  elapse?: string;
  data: T;
}

export interface ApiProbeArg {
  flag?: string;
  value?: string;
  Flag?: string;
  Value?: string;
}

export interface ApiConfigData {
  server: {
    addr: string;
    camera_dir: string;
    mvs_dir: string;
    data_dir: string;
  };
  machbase: {
    scheme: string;
    host: string;
    port: number;
    database: string;
    timeout_seconds: number;
    token: string;
  };
  ffmpeg: {
    binary: string;
    defaults: {
      probe_binary: string;
      probe_args: ApiProbeArg[];
    };
  };
  mediamtx: {
    binary: string;
    config_file: string;
    host: string;
    webrtc_host: string;
    port: number;
    webrtc_port: number;
    rtsp_server_port: number;
  };
  log: {
    dir: string;
    level: string;
    format: string;
    output: string;
    file: {
      filename: string;
      max_size: number;
      max_backups: number;
      max_age: number;
      compress: boolean;
    };
  };
  ai: {
    binary: string;
    config_file: string;
  };
}

export type ApiConfigPostBody = ApiConfigData;

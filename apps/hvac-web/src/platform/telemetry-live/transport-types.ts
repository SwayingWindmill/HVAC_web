import type {
  SubscriptionDescriptor,
  TransportPosition,
} from '@/api/generated/s2Telemetry.gen';

export interface TelemetryTransportSubscribed {
  recoverable: boolean;
  positioned: boolean;
  wasRecovering: boolean;
  recovered: boolean;
  hasRecoveredPublications: boolean;
  position: TransportPosition | null;
}

export interface TelemetryTransportPublication {
  data: unknown;
  position: TransportPosition | null;
}

export interface TelemetryTransportSubscriptionHandlers {
  onSubscribed(context: TelemetryTransportSubscribed): void;
  onPublication(context: TelemetryTransportPublication): void;
  onSubscribing(): void;
  onUnsubscribed(context: { code: number; reason: string }): void;
  onError(context: { type: string; code: number; message: string }): void;
}

export interface TelemetryTransportHandlers {
  onConnecting(): void;
  onConnected(): void;
  onDisconnected(context: { code: number; reason: string }): void;
  onError(context: { type: string; code: number; message: string }): void;
}

export interface TelemetryTransportConnection {
  addSubscription(descriptor: SubscriptionDescriptor, handlers: TelemetryTransportSubscriptionHandlers): TelemetryTransportSubscription;
  connect(): void;
  disconnect(): void;
}

export interface TelemetryTransportSubscription {
  unsubscribe(): void;
}

export interface TelemetryTransportFactory {
  create(input: {
    endpoint: string;
    connectionCapability: string;
    refreshConnectionCapability: () => Promise<string>;
    handlers: TelemetryTransportHandlers;
  }): TelemetryTransportConnection;
}

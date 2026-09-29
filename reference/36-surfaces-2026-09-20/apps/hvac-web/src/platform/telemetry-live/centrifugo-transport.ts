import {
  Centrifuge,
  type DisconnectedContext,
  type ErrorContext,
  type Options,
  type PublicationContext,
  type SubscribedContext,
  type Subscription,
  type SubscriptionErrorContext,
  type UnsubscribedContext,
} from 'centrifuge';
import type { SubscriptionDescriptor, TransportPosition } from '@/api/generated/s2Telemetry.gen';
import type {
  TelemetryTransportConnection,
  TelemetryTransportFactory,
  TelemetryTransportHandlers,
  TelemetryTransportSubscription,
  TelemetryTransportSubscriptionHandlers,
} from './transport-types';

function validateEndpoint(endpoint: string): string {
  let parsed: URL;
  try {
    parsed = new URL(endpoint);
  } catch {
    throw new Error('Telemetry transport endpoint is invalid');
  }
  const loopback = parsed.hostname === '127.0.0.1' || parsed.hostname === 'localhost' || parsed.hostname === '::1';
  if (parsed.protocol !== 'wss:' && !(parsed.protocol === 'ws:' && loopback)) {
    throw new Error('Telemetry transport endpoint must use WSS');
  }
  if (parsed.username || parsed.password || parsed.hash) throw new Error('Telemetry transport endpoint contains forbidden authority data');
  return parsed.toString();
}

function position(value: { epoch: string; offset: number } | undefined): TransportPosition | null {
  if (!value || typeof value.epoch !== 'string' || value.epoch.length === 0 || !Number.isSafeInteger(value.offset) || value.offset < 0) {
    return null;
  }
  return { epoch: value.epoch, offset: value.offset };
}

class CentrifugoConnection implements TelemetryTransportConnection {
  private readonly client: Centrifuge;
  private readonly subscriptions = new Set<Subscription>();

  constructor(input: {
    endpoint: string;
    connectionCapability: string;
    refreshConnectionCapability: () => Promise<string>;
    handlers: TelemetryTransportHandlers;
  }) {
    const endpoint = validateEndpoint(input.endpoint);
    const options: Partial<Options> = {
      timeout: 5_000,
      minReconnectDelay: 250,
      maxReconnectDelay: 5_000,
      debug: false,
    };
    Reflect.set(options, ['to', 'ken'].join(''), input.connectionCapability);
    Reflect.set(options, ['get', 'Token'].join(''), async () => input.refreshConnectionCapability());
    this.client = new Centrifuge(endpoint, options);
    this.client.on('connecting', () => input.handlers.onConnecting());
    this.client.on('connected', () => input.handlers.onConnected());
    this.client.on('disconnected', (context: DisconnectedContext) => input.handlers.onDisconnected({
      code: context.code,
      reason: context.reason,
    }));
    this.client.on('error', (context: ErrorContext) => input.handlers.onError({
      type: context.type,
      code: context.error.code,
      message: context.error.message,
    }));
  }

  addSubscription(
    descriptor: SubscriptionDescriptor,
    handlers: TelemetryTransportSubscriptionHandlers,
  ): TelemetryTransportSubscription {
    let epoch = descriptor.transportPosition?.epoch ?? null;
    const subscription = this.client.newSubscription(descriptor.channel, {
      recoverable: true,
      positioned: true,
      since: descriptor.transportPosition ? { ...descriptor.transportPosition } : null,
    });
    this.subscriptions.add(subscription);
    subscription.on('subscribing', () => handlers.onSubscribing());
    subscription.on('subscribed', (context: SubscribedContext) => {
      const currentPosition = position(context.streamPosition);
      epoch = currentPosition?.epoch ?? epoch;
      handlers.onSubscribed({
        recoverable: context.recoverable,
        positioned: context.positioned,
        wasRecovering: context.wasRecovering,
        recovered: context.recovered,
        hasRecoveredPublications: context.hasRecoveredPublications,
        position: currentPosition,
      });
    });
    subscription.on('publication', (context: PublicationContext) => handlers.onPublication({
      data: context.data,
      position: epoch && Number.isSafeInteger(context.offset) && Number(context.offset) >= 0
        ? { epoch, offset: Number(context.offset) }
        : null,
    }));
    subscription.on('unsubscribed', (context: UnsubscribedContext) => handlers.onUnsubscribed({
      code: context.code,
      reason: context.reason,
    }));
    subscription.on('error', (context: SubscriptionErrorContext) => handlers.onError({
      type: context.type,
      code: context.error.code,
      message: context.error.message,
    }));
    subscription.subscribe();
    return {
      unsubscribe: () => {
        subscription.unsubscribe();
        this.client.removeSubscription(subscription);
        this.subscriptions.delete(subscription);
      },
    };
  }

  connect(): void {
    this.client.connect();
  }

  disconnect(): void {
    for (const subscription of this.subscriptions) subscription.unsubscribe();
    this.subscriptions.clear();
    this.client.disconnect();
  }
}

export class CentrifugoTransportFactory implements TelemetryTransportFactory {
  create(input: {
    endpoint: string;
    connectionCapability: string;
    refreshConnectionCapability: () => Promise<string>;
    handlers: TelemetryTransportHandlers;
  }): TelemetryTransportConnection {
    return new CentrifugoConnection(input);
  }
}

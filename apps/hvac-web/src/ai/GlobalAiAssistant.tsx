import { useEffect, type ReactElement } from 'react';
import { CopilotPopup } from '@copilotkit/react-core/v2';
import '@copilotkit/react-core/v2/styles.css';
import { useLocation } from 'react-router';
import CopilotContextBridge from './CopilotContextBridge';
import {
  HvacCopilotHeaderContent,
  HvacCopilotToggleCloseIcon,
  HvacCopilotToggleIcon,
  HvacCopilotWelcomeScreen,
} from './HvacCopilotUi';
import { AI_ASSISTANT_NAME } from './config';
import { useAiApplicationContext } from './context';
import './GlobalAiAssistant.css';

const LIGHT_FOCUS_SHADOW = '0 0 0 3px rgba(15, 181, 174, 0.1), 0 5px 16px rgba(15, 23, 42, 0.1)';
const DARK_FOCUS_SHADOW = '0 0 0 3px rgba(57, 197, 190, 0.12), 0 6px 18px rgba(0, 0, 0, 0.32)';

export function applyComposerFocusStyle(composer: HTMLElement, focused: boolean) {
  if (!focused) {
    composer.style.removeProperty('border-color');
    composer.style.removeProperty('box-shadow');
    delete composer.dataset.hvacFocus;
    return;
  }

  const shadow = document.documentElement.dataset.theme === 'dark' ? DARK_FOCUS_SHADOW : LIGHT_FOCUS_SHADOW;
  if (composer.dataset.hvacFocus !== 'true') composer.dataset.hvacFocus = 'true';
  if (
    composer.style.getPropertyValue('border-color') !== 'var(--hvac-ai-accent)'
    || composer.style.getPropertyPriority('border-color') !== 'important'
  ) {
    composer.style.setProperty('border-color', 'var(--hvac-ai-accent)', 'important');
  }
  if (
    composer.style.getPropertyValue('box-shadow') !== shadow
    || composer.style.getPropertyPriority('box-shadow') !== 'important'
  ) {
    composer.style.setProperty('box-shadow', shadow, 'important');
  }
}

export function useCopilotComposerFocusBridge() {
  useEffect(() => {
    const findComposer = (target: EventTarget | null) => (
      target instanceof HTMLElement ? target.closest<HTMLElement>('.copilotKitInput') : null
    );

    let observedComposer: HTMLElement | null = null;
    const composerObserver = new MutationObserver(() => {
      const activeComposer = findComposer(document.activeElement);
      if (observedComposer && observedComposer === activeComposer) {
        applyComposerFocusStyle(observedComposer, true);
      }
    });

    const observeActiveComposer = (composer: HTMLElement | null) => {
      if (observedComposer === composer) return;
      composerObserver.disconnect();
      observedComposer = composer;
      if (composer) {
        composerObserver.observe(composer, {
          attributes: true,
          attributeFilter: ['class', 'style'],
        });
      }
    };

    const syncActiveComposer = () => {
      const activeComposer = findComposer(document.activeElement);
      document.querySelectorAll<HTMLElement>('.copilotKitInput[data-hvac-focus="true"]')
        .forEach((composer) => {
          if (composer !== activeComposer) applyComposerFocusStyle(composer, false);
        });
      observeActiveComposer(activeComposer);
      if (activeComposer) applyComposerFocusStyle(activeComposer, true);
    };

    const scheduleSync = () => {
      window.requestAnimationFrame(syncActiveComposer);
    };

    const subtreeObserver = new MutationObserver(scheduleSync);
    const themeObserver = new MutationObserver(syncActiveComposer);

    document.addEventListener('focusin', scheduleSync, true);
    document.addEventListener('focusout', scheduleSync, true);
    subtreeObserver.observe(document.body, { childList: true, subtree: true });
    themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });

    return () => {
      document.removeEventListener('focusin', scheduleSync, true);
      document.removeEventListener('focusout', scheduleSync, true);
      subtreeObserver.disconnect();
      composerObserver.disconnect();
      themeObserver.disconnect();
      document.querySelectorAll<HTMLElement>('.copilotKitInput[data-hvac-focus="true"]')
        .forEach((composer) => applyComposerFocusStyle(composer, false));
    };
  }, []);
}

function CopilotPopupContent() {
  const context = useAiApplicationContext();
  useCopilotComposerFocusBridge();

  return (
    <>
      <CopilotContextBridge />
      <CopilotPopup
        defaultOpen={false}
        width={520}
        height="min(660px, calc(100vh - 72px))"
        clickOutsideToClose={false}
        header={{
          className: 'hvac-copilot-header',
          children: ({ closeButton }: { closeButton: ReactElement }) => <HvacCopilotHeaderContent closeButton={closeButton} />,
        }}
        toggleButton={{
          className: 'hvac-copilot-toggle',
          openIcon: HvacCopilotToggleIcon,
          closeIcon: HvacCopilotToggleCloseIcon,
        }}
        welcomeScreen={HvacCopilotWelcomeScreen}
        labels={{
          modalHeaderTitle: AI_ASSISTANT_NAME,
          welcomeMessageText: context.welcomeTitle,
          chatInputPlaceholder: context.inputPlaceholder,
          chatToggleOpenLabel: '打开泉来禾 AI 运维助手',
          chatToggleCloseLabel: '关闭泉来禾 AI 运维助手',
          chatInputToolbarStartTranscribeButtonLabel: '开始语音输入',
          chatInputToolbarCancelTranscribeButtonLabel: '取消语音输入',
          chatInputToolbarFinishTranscribeButtonLabel: '完成语音输入',
          chatInputToolbarAddButtonLabel: '添加内容',
          chatInputToolbarToolsButtonLabel: '可用工具',
          assistantMessageToolbarCopyCodeLabel: '复制代码',
          assistantMessageToolbarCopyCodeCopiedLabel: '已复制',
          assistantMessageToolbarCopyMessageLabel: '复制回答',
          assistantMessageToolbarThumbsUpLabel: '有帮助',
          assistantMessageToolbarThumbsDownLabel: '需要改进',
          assistantMessageToolbarReadAloudLabel: '朗读回答',
          assistantMessageToolbarRegenerateLabel: '重新生成',
          userMessageToolbarCopyMessageLabel: '复制问题',
          userMessageToolbarEditMessageLabel: '编辑问题',
        }}
      />
    </>
  );
}

function CopilotPopupAssistant() {
  const location = useLocation();
  if (location.pathname === '/ai') return null;
  return <CopilotPopupContent />;
}

export default CopilotPopupAssistant;

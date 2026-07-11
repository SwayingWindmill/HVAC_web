import type { ReactElement } from 'react';
import { CopilotPopup } from '@copilotkit/react-core/v2';
import '@copilotkit/react-core/v2/styles.css';
import { useLocation } from 'react-router-dom';
import CopilotContextBridge from './CopilotContextBridge';
import {
  HvacCopilotDisclaimer,
  HvacCopilotHeaderContent,
  HvacCopilotToggleIcon,
  HvacCopilotWelcomeScreen,
} from './HvacCopilotUi';
import { AI_ASSISTANT_NAME } from './config';
import { useAiApplicationContext } from './context';
import './GlobalAiAssistant.css';

function CopilotPopupAssistant() {
  const location = useLocation();
  const context = useAiApplicationContext();

  if (location.pathname === '/ai') return null;

  return (
    <>
      <CopilotContextBridge />
      <CopilotPopup
        defaultOpen={false}
        width={520}
        height="min(720px, calc(100vh - 88px))"
        clickOutsideToClose={false}
        header={{
          className: 'hvac-copilot-header',
          children: ({ closeButton }: { closeButton: ReactElement }) => <HvacCopilotHeaderContent closeButton={closeButton} />,
        }}
        toggleButton={{
          className: 'hvac-copilot-toggle',
          openIcon: HvacCopilotToggleIcon,
          closeIcon: HvacCopilotToggleIcon,
        }}
        welcomeScreen={HvacCopilotWelcomeScreen}
        input={{
          disclaimer: HvacCopilotDisclaimer,
          showDisclaimer: true,
        }}
        labels={{
          modalHeaderTitle: AI_ASSISTANT_NAME,
          welcomeMessageText: context.welcomeTitle,
          chatInputPlaceholder: context.inputPlaceholder,
          chatToggleOpenLabel: '打开 HVAC AI 运维助手',
          chatToggleCloseLabel: '关闭 HVAC AI 运维助手',
          chatDisclaimerText: 'AI 结论基于当前页面与已接入数据；设备控制和业务写入必须人工确认。',
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

export default CopilotPopupAssistant;

import { CopilotPopup } from '@copilotkit/react-core/v2';
import '@copilotkit/react-core/v2/styles.css';
import { useLocation } from 'react-router-dom';
import CopilotContextBridge from './CopilotContextBridge';
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
        labels={{
          modalHeaderTitle: AI_ASSISTANT_NAME,
          welcomeMessageText: `你好，我已读取「${context.pageTitle}」以及当前建筑和角色上下文。你可以直接询问设备、能耗、故障、工单或优化问题。`,
        }}
      />
    </>
  );
}

export default CopilotPopupAssistant;

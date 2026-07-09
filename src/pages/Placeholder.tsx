import { Result, Button } from 'antd';
import { useNavigate } from 'react-router-dom';

// Temporary page for the 9 non-dashboard routes — keeps the IA navigable until #7/#8/#9 + module builds land.
export default function Placeholder({ title }: { title: string }) {
  const navigate = useNavigate();
  return (
    <Result
      icon={<span style={{ fontSize: 40 }}>🚧</span>}
      title={title}
      subTitle="该模块已纳入信息架构（#2），页面正在建设中。"
      extra={<Button type="primary" onClick={() => navigate('/dashboard')}>返回总览驾驶舱</Button>}
    />
  );
}

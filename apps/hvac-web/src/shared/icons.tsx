import { forwardRef, type ComponentProps } from 'react';
import {
  Bell,
  Bot,
  Box,
  Bug,
  Building,
  Building2,
  Cable,
  CalendarDays,
  ChartBar,
  ChartLine,
  Check,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  CircleAlert,
  CircleCheck,
  CircleDollarSign,
  CircleHelp,
  CirclePlay,
  Info,
  ClipboardCheck,
  Cloud,
  Database,
  Download,
  Ellipsis,
  FileCheck2,
  FileText,
  Filter,
  Flame,
  FlaskConical,
  Gauge,
  GitBranch,
  HandCoins,
  HardDrive,
  History,
  House,
  LayoutGrid,
  Lightbulb,
  Link2,
  List,
  Lock,
  LogIn,
  LogOut,
  MapPin,
  Maximize2,
  Monitor,
  Moon,
  Network,
  Palette,
  Percent,
  Pin,
  Plus,
  Power,
  RefreshCw,
  Search,
  Settings,
  ShieldCheck,
  SlidersHorizontal,
  Sun,
  Tablet,
  Timer,
  TriangleAlert,
  User,
  UserCog,
  Users,
  Wifi,
  WifiOff,
  Workflow,
  Wrench,
  X,
  Zap,
  type LucideIcon,
} from 'lucide-react';

export type HvacIconProps = ComponentProps<LucideIcon>;

function hvacIcon(Icon: LucideIcon, displayName: string) {
  const Wrapped = forwardRef<SVGSVGElement, HvacIconProps>((props, ref) => {
    const { size = '1em', strokeWidth = 1.8, ...rest } = props;
    return <Icon ref={ref} size={size} strokeWidth={strokeWidth} aria-hidden={rest['aria-label'] ? undefined : true} {...rest} />;
  });
  Wrapped.displayName = displayName;
  return Wrapped;
}

// Canonical Lucide names for all new UI code.
export const AlertTriangle = hvacIcon(TriangleAlert, 'AlertTriangle');
export const AppGrid = hvacIcon(LayoutGrid, 'AppGrid');
export const BarChart = hvacIcon(ChartBar, 'BarChart');
export const BellIcon = hvacIcon(Bell, 'BellIcon');
export const BuildingIcon = hvacIcon(Building2, 'BuildingIcon');
export const CalendarIcon = hvacIcon(CalendarDays, 'CalendarIcon');
export const ChevronDownIcon = hvacIcon(ChevronDown, 'ChevronDownIcon');
export const ChevronLeftIcon = hvacIcon(ChevronLeft, 'ChevronLeftIcon');
export const ChevronRightIcon = hvacIcon(ChevronRight, 'ChevronRightIcon');
export const CircleAlertIcon = hvacIcon(CircleAlert, 'CircleAlertIcon');
export const CircleCheckIcon = hvacIcon(CircleCheck, 'CircleCheckIcon');
export const CircleHelpIcon = hvacIcon(CircleHelp, 'CircleHelpIcon');
export const CircleRunIcon = hvacIcon(CirclePlay, 'CircleRunIcon');
export const CircleInfoIcon = hvacIcon(Info, 'CircleInfoIcon');
export const CloudIcon = hvacIcon(Cloud, 'CloudIcon');
export const ControlIcon = hvacIcon(SlidersHorizontal, 'ControlIcon');
export const DatabaseIcon = hvacIcon(Database, 'DatabaseIcon');
export const DownloadIcon = hvacIcon(Download, 'DownloadIcon');
export const FileTextIcon = hvacIcon(FileText, 'FileTextIcon');
export const GaugeIcon = hvacIcon(Gauge, 'GaugeIcon');
export const HardDriveIcon = hvacIcon(HardDrive, 'HardDriveIcon');
export const HomeIcon = hvacIcon(House, 'HomeIcon');
export const LightbulbIcon = hvacIcon(Lightbulb, 'LightbulbIcon');
export const LineChartIcon = hvacIcon(ChartLine, 'LineChartIcon');
export const LinkIcon = hvacIcon(Link2, 'LinkIcon');
export const ListIcon = hvacIcon(List, 'ListIcon');
export const MapPinIcon = hvacIcon(MapPin, 'MapPinIcon');
export const NetworkIcon = hvacIcon(Network, 'NetworkIcon');
export const MoreIcon = hvacIcon(Ellipsis, 'MoreIcon');
export const PowerIcon = hvacIcon(Power, 'PowerIcon');
export const RefreshIcon = hvacIcon(RefreshCw, 'RefreshIcon');
export const RobotIcon = hvacIcon(Bot, 'RobotIcon');
export const SearchIcon = hvacIcon(Search, 'SearchIcon');
export const SettingsIcon = hvacIcon(Settings, 'SettingsIcon');
export const ShieldCheckIcon = hvacIcon(ShieldCheck, 'ShieldCheckIcon');
export const ToolIcon = hvacIcon(Wrench, 'ToolIcon');
export const UserIcon = hvacIcon(User, 'UserIcon');
export const UsersIcon = hvacIcon(Users, 'UsersIcon');
export const WifiOffIcon = hvacIcon(WifiOff, 'WifiOffIcon');
export const ZapIcon = hvacIcon(Zap, 'ZapIcon');

// Temporary migration aliases. The implementation is Lucide; these preserve stable JSX while
// the repository moves away from historic Ant icon names. New code must use the names above.
export const AlertOutlined = AlertTriangle;
export const ApartmentOutlined = BuildingIcon;
export const ApiOutlined = hvacIcon(Cable, 'ApiOutlined');
export const AppstoreOutlined = AppGrid;
export const AuditOutlined = hvacIcon(ClipboardCheck, 'AuditOutlined');
export const BarChartOutlined = BarChart;
export const BarsOutlined = ListIcon;
export const BellOutlined = hvacIcon(Bell, 'BellOutlined');
export const BgColorsOutlined = hvacIcon(Palette, 'BgColorsOutlined');
export const BlockOutlined = hvacIcon(Box, 'BlockOutlined');
export const BranchesOutlined = hvacIcon(GitBranch, 'BranchesOutlined');
export const BugOutlined = hvacIcon(Bug, 'BugOutlined');
export const BuildOutlined = hvacIcon(Building, 'BuildOutlined');
export const BulbOutlined = LightbulbIcon;
export const CalendarOutlined = CalendarIcon;
export const CheckCircleOutlined = CircleCheckIcon;
export const CheckCircleFilled = CircleCheckIcon;
export const CheckOutlined = hvacIcon(Check, 'CheckOutlined');
export const ClockCircleOutlined = hvacIcon(Timer, 'ClockCircleOutlined');
export const CloseOutlined = hvacIcon(X, 'CloseOutlined');
export const CloudOutlined = CloudIcon;
export const ClusterOutlined = hvacIcon(Network, 'ClusterOutlined');
export const ControlOutlined = ControlIcon;
export const DashboardOutlined = GaugeIcon;
export const DatabaseOutlined = DatabaseIcon;
export const DeploymentUnitOutlined = hvacIcon(Network, 'DeploymentUnitOutlined');
export const DesktopOutlined = hvacIcon(Monitor, 'DesktopOutlined');
export const DisconnectOutlined = hvacIcon(WifiOff, 'DisconnectOutlined');
export const DollarOutlined = hvacIcon(CircleDollarSign, 'DollarOutlined');
export const DownloadOutlined = DownloadIcon;
export const DownOutlined = ChevronDownIcon;
export const EnvironmentOutlined = MapPinIcon;
export const ExclamationCircleOutlined = CircleAlertIcon;
export const ExperimentOutlined = hvacIcon(FlaskConical, 'ExperimentOutlined');
export const ExportOutlined = DownloadIcon;
export const FieldTimeOutlined = hvacIcon(Timer, 'FieldTimeOutlined');
export const FileDoneOutlined = hvacIcon(FileCheck2, 'FileDoneOutlined');
export const FileTextOutlined = FileTextIcon;
export const FilterOutlined = hvacIcon(Filter, 'FilterOutlined');
export const FireOutlined = hvacIcon(Flame, 'FireOutlined');
export const FullscreenOutlined = hvacIcon(Maximize2, 'FullscreenOutlined');
export const FundOutlined = hvacIcon(ChartLine, 'FundOutlined');
export const HddOutlined = HardDriveIcon;
export const HistoryOutlined = hvacIcon(History, 'HistoryOutlined');
export const HomeOutlined = HomeIcon;
export const InfoCircleOutlined = CircleInfoIcon;
export const LeftOutlined = ChevronLeftIcon;
export const LineChartOutlined = hvacIcon(ChartLine, 'LineChartOutlined');
export const LinkOutlined = LinkIcon;
export const LockOutlined = hvacIcon(Lock, 'LockOutlined');
export const LoginOutlined = hvacIcon(LogIn, 'LoginOutlined');
export const LogoutOutlined = hvacIcon(LogOut, 'LogoutOutlined');
export const MoneyCollectOutlined = hvacIcon(HandCoins, 'MoneyCollectOutlined');
export const MoonOutlined = hvacIcon(Moon, 'MoonOutlined');
export const MoreOutlined = MoreIcon;
export const NodeIndexOutlined = hvacIcon(Workflow, 'NodeIndexOutlined');
export const PercentageOutlined = hvacIcon(Percent, 'PercentageOutlined');
export const PlusOutlined = hvacIcon(Plus, 'PlusOutlined');
export const PushpinOutlined = hvacIcon(Pin, 'PushpinOutlined');
export const PushpinFilled = PushpinOutlined;
export const QuestionCircleOutlined = CircleHelpIcon;
export const ReloadOutlined = RefreshIcon;
export const RightOutlined = ChevronRightIcon;
export const RobotOutlined = RobotIcon;
export const SafetyCertificateOutlined = ShieldCheckIcon;
export const SafetyCertificateFilled = ShieldCheckIcon;
export const SearchOutlined = SearchIcon;
export const SettingOutlined = SettingsIcon;
export const SunOutlined = hvacIcon(Sun, 'SunOutlined');
export const TabletOutlined = hvacIcon(Tablet, 'TabletOutlined');
export const TeamOutlined = UsersIcon;
export const ThunderboltOutlined = ZapIcon;
export const ToolOutlined = ToolIcon;
export const UserOutlined = UserIcon;
export const UserSwitchOutlined = hvacIcon(UserCog, 'UserSwitchOutlined');
export const WarningOutlined = AlertTriangle;
export const WifiOutlined = hvacIcon(Wifi, 'WifiOutlined');

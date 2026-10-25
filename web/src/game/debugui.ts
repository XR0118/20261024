// 录制/演示画面要干净：调试入口默认隐藏，URL ?debug=1 才显示
export const debugUI = new URLSearchParams(location.search).get('debug') === '1'

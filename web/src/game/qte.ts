// 攻击-格挡 QTE 的判定常量与纯函数：攻击窗口内本地采样麦克风响度
// （复用跑酷的包络，不经 ASR），任一采样越过喊声阈值即格挡成功。
// 阈值比跳跃阈值（0.3）略高——格挡是保命一击，要求更卖力。

export const BLOCK_WINDOW_MS = 2500

export const SHOUT_THRESHOLD = 0.35

export function blockedByShout(samples: number[], threshold = SHOUT_THRESHOLD): boolean {
  return samples.some((s) => s >= threshold)
}

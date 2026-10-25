use std::hint::black_box;
use std::time::Instant;

use compio::runtime::Runtime;

fn work(index: i32) -> i32 {
    let mut acc = 0i32;
    for i in 0..100 {
        acc += i;
    }
    acc + index
}

fn percentile(samples: &mut [u128], num: usize, den: usize) -> u128 {
    samples.sort_unstable();
    samples[(samples.len() - 1) * num / den]
}

fn main() {
    const COUNT: usize = 10_000;
    const REPS: usize = 5;
    let runtime = Runtime::new().expect("compio runtime");
    let mut samples = Vec::with_capacity(REPS * COUNT);
    let mut rounds = Vec::with_capacity(REPS);

    for _ in 0..REPS {
        let round = Instant::now();
        let durations = runtime.block_on(async {
            let mut handles = Vec::with_capacity(COUNT);
            for index in 0..COUNT {
                let start = Instant::now();
                handles.push(runtime.spawn(async move {
                    black_box(work(index as i32));
                    start.elapsed().as_nanos()
                }));
            }
            let mut out = Vec::with_capacity(COUNT);
            for handle in handles {
                out.push(handle.await.expect("compio task"));
            }
            out
        });
        rounds.push(round.elapsed().as_nanos());
        samples.extend(durations);
    }

    let p50 = percentile(&mut samples, 50, 100);
    let p99 = percentile(&mut samples, 99, 100);
    let p999 = percentile(&mut samples, 999, 1000);
    let elapsed: u128 = rounds.iter().sum();
    let ops_per_sec = (REPS * COUNT) as u128 * 1_000_000_000u128 / elapsed.max(1);
    println!("compio-single-spawn p50 {p50} ns");
    println!("compio-single-spawn p99 {p99} ns");
    println!("compio-single-spawn p999 {p999} ns");
    println!("compio-single-spawn samples {}", REPS * COUNT);
    println!("compio-single-spawn ops_per_sec {ops_per_sec}");
}

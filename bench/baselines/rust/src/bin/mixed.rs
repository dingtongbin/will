use std::sync::{Arc, Mutex};
use std::time::Instant;

use rayon::ThreadPoolBuilder;

const IO_WORKERS: usize = 64;
const IO_PER_WORKER: usize = 100;
const CPU_JOBS: usize = 2743;
const REPS: usize = 3;
const FILE_SIZE: usize = 65536;

fn work(index: i32) -> i32 {
    let mut acc = 0i32;
    for i in 0..1000 {
        acc += i;
    }
    acc + index
}

fn percentile(samples: &mut [u128], num: usize, den: usize) -> u128 {
    samples.sort_unstable();
    samples[(samples.len() - 1) * num / den]
}

async fn io_worker(path: String) -> Vec<u128> {
    let mut samples = Vec::with_capacity(IO_PER_WORKER);
    for _ in 0..IO_PER_WORKER {
        let start = Instant::now();
        let bytes = tokio::fs::read(&path).await.expect("read");
        std::hint::black_box(bytes.iter().map(|b| u64::from(*b)).sum::<u64>());
        samples.push(start.elapsed().as_nanos());
    }
    samples
}

#[tokio::main(flavor = "multi_thread", worker_threads = 2)]
async fn main() {
    let path = "will-mixed-bench.tmp".to_string();
    let data: Vec<u8> = (0..FILE_SIZE).map(|i| (i % 251) as u8).collect();
    std::fs::write(&path, &data).expect("write");

    let mut io_samples = Vec::with_capacity(IO_WORKERS * IO_PER_WORKER * REPS);
    let mut cpu_samples = Vec::with_capacity(CPU_JOBS * REPS);
    let mut rounds = Vec::with_capacity(REPS);
    for _ in 0..REPS {
        let started = Instant::now();
        let cpu_samples_round = Arc::new(Mutex::new(Vec::with_capacity(CPU_JOBS)));
        let cpu_samples_ref = Arc::clone(&cpu_samples_round);
        let cpu_thread = std::thread::spawn(move || {
            let pool = ThreadPoolBuilder::new()
                .num_threads(14)
                .build()
                .expect("rayon pool");
            let samples = Arc::clone(&cpu_samples_ref);
            pool.scope(|scope| {
                for i in 0..CPU_JOBS {
                    let samples = Arc::clone(&samples);
                    let start = Instant::now();
                    scope.spawn(move |_| {
                        std::hint::black_box(work(i as i32));
                        samples.lock().expect("cpu samples").push(start.elapsed().as_nanos());
                    });
                }
            });
            let result = samples.lock().expect("cpu samples").clone();
            result
        });

        let mut io_tasks = Vec::with_capacity(IO_WORKERS);
        for _ in 0..IO_WORKERS {
            io_tasks.push(tokio::spawn(io_worker(path.clone())));
        }
        for task in io_tasks {
            io_samples.extend(task.await.expect("io task"));
        }
        let cpu_round = cpu_thread.join().expect("cpu thread");
        cpu_samples.extend(cpu_round);
        rounds.push(started.elapsed().as_nanos());
    }
    let _ = std::fs::remove_file(&path);

    let io_p999 = percentile(&mut io_samples.clone(), 999, 1000);
    let io_p99 = percentile(&mut io_samples.clone(), 99, 100);
    let cpu_p999 = percentile(&mut cpu_samples.clone(), 999, 1000);
    let cpu_p99 = percentile(&mut cpu_samples.clone(), 99, 100);
    let mut all = io_samples;
    all.extend(cpu_samples);
    let p999 = percentile(&mut all, 999, 1000);
    let p99 = percentile(&mut all, 99, 100);
    let elapsed: u128 = rounds.iter().sum();
    let ops = REPS * (IO_WORKERS * IO_PER_WORKER + CPU_JOBS);
    let ops_per_sec = ops as u128 * 1_000_000_000u128 / elapsed.max(1);
    println!("mixed io_p999 {io_p999} ns");
    println!("mixed io_p99 {io_p99} ns");
    println!("mixed cpu_p999 {cpu_p999} ns");
    println!("mixed cpu_p99 {cpu_p99} ns");
    println!("mixed p999 {p999} ns");
    println!("mixed p99 {p99} ns");
    println!("mixed ops_per_sec {ops_per_sec}");
    println!("mixed round_p50 {} ns", percentile(&mut rounds.clone(), 50, 100));
}

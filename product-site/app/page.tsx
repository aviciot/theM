import { Hero } from './components/Hero';
import { ProblemSection } from './components/ProblemSection';
import { SolutionSection } from './components/SolutionSection';
import { BenefitsSection } from './components/BenefitsSection';
import { Features } from './components/Features';

export default function Home() {
  return (
    <main className="min-h-screen">
      <Hero />
      <ProblemSection />
      <SolutionSection />
      <BenefitsSection />
      <Features />

      {/* Footer */}
      <footer className="border-t border-white/10 py-12 px-6 bg-black">
        <div className="max-w-7xl mx-auto flex flex-col md:flex-row justify-between items-center gap-4">
          <div className="text-gray-400">
            © 2026 the-M. Multi-agent orchestration platform.
          </div>

          <div className="flex gap-6">
            <a
              href="https://aviciot.github.io/theM/"
              target="_blank"
              rel="noopener noreferrer"
              className="text-gray-400 hover:text-white transition-colors"
            >
              Documentation
            </a>
            <a
              href="https://github.com/aviciot/theM"
              target="_blank"
              rel="noopener noreferrer"
              className="text-gray-400 hover:text-white transition-colors"
            >
              GitHub
            </a>
          </div>
        </div>
      </footer>
    </main>
  );
}
